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

var runEntireEnable = func(repoRoot string, localOnly bool, out io.Writer) error {
	AddToolDirsToPath()
	args := []string{"enable", "--agent", AgentName, "--checkpoint-push-remote", CheckpointRemote}
	if localOnly {
		args = []string{"enable", "--agent", AgentName, "--local", "--skip-push-sessions"}
	}
	cmd := exec.Command("entire", args...)
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
//
// An empty checkpointRepo falls back to the GitHub checkpoint_remote committed in
// .entire/settings.json, so hooks can bootstrap fresh clones on their own. Without either, the
// clone is bootstrapped local-only: Devin sessions are recorded, but checkpoints are never pushed,
// so transcripts cannot reach origin.
func Bootstrap(repoRoot, checkpointRepo string, out io.Writer) error {
	if checkpointRepo == "" {
		checkpointRepo = committedCheckpointRepo(repoRoot)
	}
	localOnly := checkpointRepo == ""
	if !localOnly && (strings.Count(checkpointRepo, "/") != 1 || strings.HasPrefix(checkpointRepo, "/") || strings.HasSuffix(checkpointRepo, "/")) {
		return fmt.Errorf("checkpoint repo must be owner/repo, got %q", checkpointRepo)
	}
	top, err := gitOut(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s is not a git repository: %w", repoRoot, err)
	}
	repoRoot = top
	if err := pinCheckpointRemote(repoRoot, checkpointRepo); err != nil {
		return err
	}
	if err := allowExternalAgents(repoRoot); err != nil {
		return err
	}
	if err := privatizeTrackedHooks(repoRoot); err != nil {
		return err
	}
	restore := keepTrackedEntireHooks(repoRoot)
	err = runEntireEnable(repoRoot, localOnly, out)
	restore()
	if err != nil {
		return err
	}
	// The local-only marker is written last so a failed enable is retried by the next hook.
	if localOnly {
		if err := keepCheckpointsLocal(repoRoot); err != nil {
			return err
		}
	}
	if err := excludeUntracked(repoRoot, ".devin", ".entire"); err != nil {
		return err
	}
	requeueFailedPayloads(repoRoot)
	return nil
}

// requeueFailedPayloads moves payloads Entire rejected before the clone was bootstrapped back
// into the pending queue, so the next hook replays them.
func requeueFailedPayloads(repoRoot string) {
	q := filepath.Join(repoRoot, ".entire", "tmp", "devin-pending")
	entries, err := os.ReadDir(filepath.Join(q, "failed"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			_ = os.Rename(filepath.Join(q, "failed", e.Name()), filepath.Join(q, e.Name()))
		}
	}
}

func pinCheckpointRemote(repoRoot, checkpointRepo string) error {
	if checkpointRepo == "" {
		return nil
	}
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
	return nil
}

// keepTrackedEntireHooks snapshots a committed .devin/hooks.v1.json that already runs Entire's
// Devin hooks and returns a func restoring it, so `entire enable` never leaves the tracked file
// modified (older installers append a second hook group to it).
func keepTrackedEntireHooks(repoRoot string) func() {
	path := HooksFile(repoRoot)
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || !hasEntireHooks(repoRoot) {
		return func() {}
	}
	if tracked, err := gitOut(repoRoot, "ls-files", "--", filepath.ToSlash(rel)); err != nil || tracked == "" {
		return func() {}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return func() {}
	}
	info, _ := os.Stat(path)
	return func() {
		if cur, err := os.ReadFile(path); err == nil && string(cur) == string(data) {
			return
		}
		_ = os.WriteFile(path, data, info.Mode().Perm())
	}
}

type localSettings struct {
	ExternalAgents  bool `json:"external_agents"`
	StrategyOptions struct {
		CheckpointPushRemote string `json:"checkpoint_push_remote"`
		PushSessions         *bool  `json:"push_sessions"`
	} `json:"strategy_options"`
}

func readLocalSettings(repoRoot string) (localSettings, bool) {
	var s localSettings
	data, err := os.ReadFile(filepath.Join(repoRoot, ".entire", "settings.local.json"))
	if err != nil || json.Unmarshal(data, &s) != nil {
		return s, false
	}
	return s, true
}

// NeedsBootstrap reports whether the clone at repoRoot lacks the untracked settings that let
// Entire load the Devin agent with a safe checkpoint destination.
func NeedsBootstrap(repoRoot string) bool {
	s, ok := readLocalSettings(repoRoot)
	if !ok || !s.ExternalAgents {
		return true
	}
	o := s.StrategyOptions
	return o.CheckpointPushRemote == "" && (o.PushSessions == nil || *o.PushSessions)
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
	gitDir, err := gitOut(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	private := filepath.Join(gitDir, "entire-hooks")
	if err := os.MkdirAll(private, 0o755); err != nil {
		return err
	}
	// Wrap every client hook name, not just the ones present now, so hooks committed or removed
	// later take effect without re-running bootstrap.
	for _, name := range clientHooks {
		dst := filepath.Join(private, name)
		if exists(dst) || exists(dst+".pre-entire") {
			continue
		}
		wrapper := fmt.Sprintf("#!/bin/sh\nh=\"$(git rev-parse --show-toplevel)/%s\"\n[ -x \"$h\" ] || exit 0\nexec \"$h\" \"$@\"\n", filepath.ToSlash(filepath.Join(rel, name)))
		if err := os.WriteFile(dst, []byte(wrapper), 0o755); err != nil {
			return err
		}
	}
	return git(repoRoot, "config", "--local", "core.hooksPath", private)
}

var clientHooks = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit",
	"prepare-commit-msg", "commit-msg", "post-commit", "pre-rebase", "post-checkout", "post-merge",
	"pre-push", "post-rewrite", "pre-auto-gc", "push-to-checkout", "sendemail-validate",
	"reference-transaction", "post-index-change",
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
// It also selects the per-checkpoint git-refs backend: with the shared entire/checkpoints/v1
// branch, clones on different Devin VMs diverge and only one of them can push its checkpoints.
// Local-only clones also disable session pushes, so checkpoints never fall back to origin.
func allowExternalAgents(repoRoot string) error {
	return updateLocalSettings(repoRoot, func(settings map[string]any) {
		settings["external_agents"] = true
		checkpoints, _ := settings["checkpoints"].(map[string]any)
		if checkpoints == nil {
			checkpoints = map[string]any{}
		}
		primary, _ := checkpoints["primary"].(map[string]any)
		if primary == nil {
			primary = map[string]any{}
		}
		primary["type"] = "git-refs"
		checkpoints["primary"] = primary
		settings["checkpoints"] = checkpoints
	})
}

// keepCheckpointsLocal turns off checkpoint pushing unless a checkpoint remote is pinned.
func keepCheckpointsLocal(repoRoot string) error {
	return updateLocalSettings(repoRoot, func(settings map[string]any) {
		opts, _ := settings["strategy_options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		if _, pinned := opts["checkpoint_push_remote"]; !pinned {
			opts["push_sessions"] = false
		}
		settings["strategy_options"] = opts
	})
}

func updateLocalSettings(repoRoot string, update func(map[string]any)) error {
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
	update(settings)
	data, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// committedCheckpointRepo returns owner/repo of the GitHub checkpoint_remote in the
// repository's committed .entire/settings.json, or "" when there is none.
func committedCheckpointRepo(repoRoot string) string {
	top, err := gitOut(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(top, ".entire", "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		StrategyOptions struct {
			CheckpointRemote struct {
				Provider string `json:"provider"`
				Repo     string `json:"repo"`
			} `json:"checkpoint_remote"`
		} `json:"strategy_options"`
	}
	if json.Unmarshal(data, &settings) != nil || settings.StrategyOptions.CheckpointRemote.Provider != "github" {
		return ""
	}
	return settings.StrategyOptions.CheckpointRemote.Repo
}
