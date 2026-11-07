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
		if err := git(repoRoot, "config", "--replace-all", "remote."+CheckpointRemote+".url", url); err != nil {
			return err
		}
		// Extra urls or a leftover pushurl would keep sending checkpoints to the old destination.
		_ = git(repoRoot, "config", "--unset-all", "remote."+CheckpointRemote+".pushurl")
	}
	if err := allowExternalAgents(repoRoot); err != nil {
		return err
	}
	if err := privatizeTrackedHooks(repoRoot); err != nil {
		return err
	}
	if err := runEntireEnable(repoRoot, out); err != nil {
		return err
	}
	return excludeUntracked(repoRoot, ".devin", ".entire")
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}

// privatizeTrackedHooks keeps `entire enable` from rewriting hooks committed to the repository
// (core.hooksPath pointing at a tracked directory such as .githooks). It points core.hooksPath at
// a per-clone directory whose wrappers exec the tracked hooks, so they still run and stay current,
// and Entire installs its hooks there instead.
func privatizeTrackedHooks(repoRoot string) error {
	hooks, err := gitOut(repoRoot, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(repoRoot, hooks)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil
	}
	if tracked, err := gitOut(repoRoot, "ls-files", "--", rel); err != nil || tracked == "" {
		return err
	}
	gitDir, err := gitOut(repoRoot, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return err
	}
	private := filepath.Join(gitDir, "entire-hooks")
	if err := os.MkdirAll(private, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(hooks)
	if err != nil {
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || strings.Contains(e.Name(), ".") {
			continue
		}
		dst := filepath.Join(private, e.Name())
		if exists(dst) || exists(dst+".pre-entire") {
			continue
		}
		wrapper := fmt.Sprintf("#!/bin/sh\nexec \"$(git rev-parse --show-toplevel)/%s\" \"$@\"\n", filepath.ToSlash(filepath.Join(rel, e.Name())))
		if err := os.WriteFile(dst, []byte(wrapper), 0o755); err != nil {
			return err
		}
	}
	return git(repoRoot, "config", "--local", "core.hooksPath", private)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// excludeUntracked adds untracked files under paths to .git/info/exclude so the files Entire
// generates are never committed by accident.
func excludeUntracked(repoRoot string, paths ...string) error {
	files, err := gitOut(repoRoot, append([]string{"ls-files", "--others", "--exclude-standard", "--"}, paths...)...)
	if err != nil || files == "" {
		return err
	}
	exclude, err := gitOut(repoRoot, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	data, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		have[l] = true
	}
	var add strings.Builder
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		add.WriteString("\n")
	}
	for _, f := range strings.Split(files, "\n") {
		if !have["/"+f] {
			add.WriteString("/" + f + "\n")
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fh.WriteString(add.String()); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
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
