package devin

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorRepo(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	check := func() (fails []string) {
		doctorRepo(repo, func(ok bool, format string, args ...any) {
			if !ok {
				fails = append(fails, format)
			}
		})
		return fails
	}
	if fails := check(); len(fails) != 2 {
		t.Fatalf("fresh clone failures = %q", fails)
	}
	if _, err := InstallHooks(repo, false); err != nil {
		t.Fatal(err)
	}
	if err := allowExternalAgents(repo); err != nil {
		t.Fatal(err)
	}
	if err := keepCheckpointsLocal(repo); err != nil {
		t.Fatal(err)
	}
	if fails := check(); len(fails) != 0 {
		t.Fatalf("bootstrapped clone failures = %q", fails)
	}
	pinned := `{"external_agents": true, "strategy_options": {"checkpoint_push_remote": "checkpoints"}}`
	if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.local.json"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	if fails := check(); len(fails) != 1 || !strings.Contains(fails[0], "missing") {
		t.Fatalf("failures without the checkpoint remote = %q", fails)
	}
	if out, err := exec.Command("git", "-C", repo, "remote", "add", CheckpointRemote, "https://github.com/me/ckpt.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v: %s", err, out)
	}
	if fails := check(); len(fails) != 0 {
		t.Fatalf("pinned clone failures = %q", fails)
	}
	failed := filepath.Join(repo, ".entire", "tmp", "devin-pending", "failed")
	if err := os.MkdirAll(failed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failed, "1-1.stop"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fails := check(); len(fails) != 1 || !strings.Contains(fails[0], "rejected") {
		t.Fatalf("failures with a rejected payload = %q", fails)
	}
}

func TestBootstrapDispatchTargets(t *testing.T) {
	repos := t.TempDir()
	optedIn, plain := filepath.Join(repos, "a"), filepath.Join(repos, "b")
	for _, r := range []string{optedIn, plain} {
		if out, err := exec.Command("git", "init", "-q", r).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
	}
	if _, err := InstallHooks(optedIn, false); err != nil {
		t.Fatal(err)
	}
	var enabled []string
	orig := runEntireEnable
	runEntireEnable = func(root string, _ bool, _ io.Writer) error { enabled = append(enabled, root); return nil }
	t.Cleanup(func() { runEntireEnable = orig })
	t.Chdir(repos)
	t.Setenv("DEVIN_PROJECT_DIR", "")
	t.Setenv("ENTIRE_DEVIN_REPOS", filepath.Join(repos, "*"))

	for i := 0; i < 2; i++ {
		got := BootstrapDispatchTargets(io.Discard)
		if len(got) != 1 || filepath.Base(got[0]) != "a" {
			t.Fatalf("targets = %v", got)
		}
	}
	if len(enabled) != 1 || filepath.Base(enabled[0]) != "a" {
		t.Fatalf("bootstrapped %v, want only the opted-in repo once", enabled)
	}
	if !NeedsBootstrap(plain) {
		t.Fatal("repository without Entire was bootstrapped")
	}
}
