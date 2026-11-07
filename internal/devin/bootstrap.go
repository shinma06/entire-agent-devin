package devin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckpointRemote is the git remote that receives Entire checkpoints in a bootstrapped clone.
const CheckpointRemote = "checkpoints"

var runEntireEnable = func(repoRoot string, out io.Writer) error {
	cmd := exec.Command("entire", "enable", "--agent", AgentName, "--checkpoint-push-remote", CheckpointRemote)
	cmd.Dir = repoRoot
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// Bootstrap prepares a fresh clone for Devin sessions: it points the `checkpoints` remote at
// checkpointRepo (owner/repo on GitHub), allows external agents locally, and enables Entire's
// Devin hooks with checkpoints pinned to that remote. Pinning matters in Devin clones, whose
// origin is a git proxy URL that Entire cannot match to the checkpoint repository's owner; it
// would otherwise push transcripts to origin.
func Bootstrap(repoRoot, checkpointRepo string, out io.Writer) error {
	if strings.Count(checkpointRepo, "/") != 1 || strings.HasPrefix(checkpointRepo, "/") || strings.HasSuffix(checkpointRepo, "/") {
		return fmt.Errorf("checkpoint repo must be owner/repo, got %q", checkpointRepo)
	}
	top, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("%s is not a git repository: %w", repoRoot, err)
	}
	repoRoot = strings.TrimSpace(string(top))
	url := "https://github.com/" + checkpointRepo + ".git"
	if err := git(repoRoot, "remote", "get-url", CheckpointRemote); err != nil {
		if err := git(repoRoot, "remote", "add", CheckpointRemote, url); err != nil {
			return err
		}
	} else {
		if err := git(repoRoot, "remote", "set-url", CheckpointRemote, url); err != nil {
			return err
		}
		// A leftover pushurl would keep sending checkpoints to the old destination.
		_ = git(repoRoot, "config", "--unset-all", "remote."+CheckpointRemote+".pushurl")
	}
	if err := allowExternalAgents(repoRoot); err != nil {
		return err
	}
	return runEntireEnable(repoRoot, out)
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return nil
}

// allowExternalAgents sets external_agents in .entire/settings.local.json, keeping other keys.
func allowExternalAgents(repoRoot string) error {
	path := filepath.Join(repoRoot, ".entire", "settings.local.json")
	settings := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	settings["external_agents"] = true
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
