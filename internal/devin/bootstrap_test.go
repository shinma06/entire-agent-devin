package devin

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrap(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".entire"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.local.json"), []byte(`{"telemetry": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled := 0
	orig := runEntireEnable
	runEntireEnable = func(string, bool, io.Writer) error { enabled++; return nil }
	t.Cleanup(func() { runEntireEnable = orig })

	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(repo, "me/old", io.Discard); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "set-url", "--push", CheckpointRemote, "https://example.com/public.git").CombinedOutput(); err != nil {
		t.Fatalf("set pushurl: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "set-url", "--add", CheckpointRemote, "https://example.com/extra.git").CombinedOutput(); err != nil {
		t.Fatalf("add url: %v: %s", err, out)
	}
	committed := `{"strategy_options":{"checkpoint_remote":{"provider":"github","repo":"me/ckpt"}}}`
	if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.json"), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(sub, "", io.Discard); err != nil {
		t.Fatalf("Bootstrap from committed settings: %v", err)
	}
	out, err := exec.Command("git", "-C", repo, "config", "--get-all", "remote."+CheckpointRemote+".url").Output()
	if err != nil || strings.TrimSpace(string(out)) != "https://github.com/me/ckpt.git" {
		t.Fatalf("checkpoints remote = %q, %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "config", "--get", "remote."+CheckpointRemote+".pushurl").Output(); err == nil {
		t.Fatalf("stale pushurl kept: %s", out)
	}
	if _, err := os.Stat(filepath.Join(sub, ".entire")); err == nil {
		t.Fatal("bootstrap wrote settings into the subdirectory")
	}
	var s map[string]any
	data, _ := os.ReadFile(filepath.Join(repo, ".entire", "settings.local.json"))
	if err := json.Unmarshal(data, &s); err != nil || s["external_agents"] != true || s["telemetry"] != false {
		t.Fatalf("settings = %s, %v", data, err)
	}
	if primary, _ := s["checkpoints"].(map[string]any)["primary"].(map[string]any); primary["type"] != "git-refs" {
		t.Fatalf("checkpoint backend = %s", data)
	}
	if enabled != 2 {
		t.Fatalf("entire enable ran %d times, want 2", enabled)
	}
	if err := Bootstrap(t.TempDir(), "", io.Discard); err == nil {
		t.Fatal("Bootstrap ran without a checkpoint repo")
	}
	if err := Bootstrap(repo, "bad", io.Discard); err == nil {
		t.Fatal("Bootstrap accepted a repo without owner")
	}
}

func TestBootstrapLocalOnly(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if !NeedsBootstrap(repo) {
		t.Fatal("fresh clone does not need bootstrap")
	}
	var localOnly []bool
	enableErr := errors.New("entire enable failed")
	orig := runEntireEnable
	runEntireEnable = func(_ string, local bool, _ io.Writer) error { localOnly = append(localOnly, local); return enableErr }
	t.Cleanup(func() { runEntireEnable = orig })

	if err := Bootstrap(repo, "", io.Discard); err == nil {
		t.Fatal("Bootstrap ignored a failed entire enable")
	}
	if !NeedsBootstrap(repo) {
		t.Fatal("failed local-only bootstrap is not retried")
	}
	q := filepath.Join(repo, ".entire", "tmp", "devin-pending")
	if err := os.MkdirAll(filepath.Join(q, "failed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q, "failed", "1-1.stop"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	enableErr, localOnly = nil, nil
	if err := Bootstrap(repo, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q, "1-1.stop")); err != nil {
		t.Fatalf("rejected payload not requeued: %v", err)
	}
	if len(localOnly) != 1 || !localOnly[0] {
		t.Fatalf("entire enable local-only calls = %v", localOnly)
	}
	if err := exec.Command("git", "-C", repo, "remote", "get-url", CheckpointRemote).Run(); err == nil {
		t.Fatal("local-only bootstrap added a checkpoints remote")
	}
	var s struct {
		ExternalAgents  bool           `json:"external_agents"`
		StrategyOptions map[string]any `json:"strategy_options"`
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".entire", "settings.local.json"))
	if err := json.Unmarshal(data, &s); err != nil || !s.ExternalAgents || s.StrategyOptions["push_sessions"] != false {
		t.Fatalf("settings = %s, %v", data, err)
	}
	if NeedsBootstrap(repo) {
		t.Fatal("local-only clone still needs bootstrap")
	}
	if err := Bootstrap(repo, "me/ckpt", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(localOnly) != 2 || localOnly[1] {
		t.Fatalf("entire enable local-only calls = %v", localOnly)
	}
}

func TestNeedsBootstrap(t *testing.T) {
	for local, want := range map[string]bool{
		`{}`:                       true,
		`{"external_agents":true}`: true,
		`not json`:                 true,
		`{"external_agents":false,"strategy_options":{"push_sessions":false}}`:                 true,
		`{"external_agents":true,"strategy_options":{"push_sessions":true}}`:                   true,
		`{"external_agents":true,"strategy_options":{"push_sessions":false}}`:                  false,
		`{"external_agents":true,"strategy_options":{"checkpoint_push_remote":"checkpoints"}}`: false,
	} {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, ".entire"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.local.json"), []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := NeedsBootstrap(repo); got != want {
			t.Errorf("NeedsBootstrap(%s) = %v, want %v", local, got, want)
		}
	}
}

func TestBootstrapRestoresTrackedEntireHooks(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if _, err := InstallHooks(repo, false); err != nil {
		t.Fatal(err)
	}
	committed, _ := os.ReadFile(HooksFile(repo))
	run("add", ".devin")
	run("commit", "-qm", "hooks")
	orig := runEntireEnable
	runEntireEnable = func(root string, _ bool, _ io.Writer) error {
		return os.WriteFile(HooksFile(root), []byte("rewritten\n"), 0o644)
	}
	t.Cleanup(func() { runEntireEnable = orig })
	if err := Bootstrap(repo, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(HooksFile(repo)); string(got) != string(committed) {
		t.Fatalf("tracked hooks not restored: %q", got)
	}
}

func TestBootstrapKeepsTrackedHooks(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, ".githooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".githooks", "pre-commit"), []byte("#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/.git/ran\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("add", ".githooks")
	run("commit", "-qm", "hooks", "--no-verify")
	run("config", "--local", "core.hooksPath", ".githooks")

	orig := runEntireEnable
	runEntireEnable = func(root string, _ bool, _ io.Writer) error {
		hooks := run("rev-parse", "--path-format=absolute", "--git-path", "hooks")
		if err := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, ".devin"), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, ".devin", "hooks.v1.json"), []byte("{}"), 0o644)
	}
	t.Cleanup(func() { runEntireEnable = orig })

	for i := 0; i < 2; i++ {
		if err := Bootstrap(repo, "me/ckpt", io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if got := run("status", "--porcelain", "--untracked-files=all"); got != "" {
		t.Fatalf("working tree changed:\n%s", got)
	}
	if got := run("config", "--get", "core.hooksPath"); !strings.HasSuffix(got, filepath.Join(".git", "entire-hooks")) {
		t.Fatalf("core.hooksPath = %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); strings.Count(string(data), "/.devin/hooks.v1.json") != 1 {
		t.Fatalf("exclude = %q", data)
	}
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-qm", "f")
	if _, err := os.Stat(filepath.Join(repo, ".git", "ran")); err != nil {
		t.Fatal("tracked pre-commit hook did not run through the wrapper")
	}
	if err := os.WriteFile(filepath.Join(repo, ".githooks", "commit-msg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "commit", "-q", "--allow-empty", "-m", "blocked")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if err := cmd.Run(); err == nil {
		t.Fatal("hook added after bootstrap did not run")
	}
}
