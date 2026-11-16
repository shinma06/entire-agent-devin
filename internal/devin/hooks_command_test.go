package devin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runHookCommand(t *testing.T, repo, home, hook, payload string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", hookCommand(hook))
	cmd.Dir = repo
	cmd.Env = []string{"PATH=" + filepath.Dir(gitPath(t)) + ":/usr/bin:/bin", "HOME=" + home, "LOG=" + filepath.Join(home, "log")}
	cmd.Stdin = strings.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: hook command must exit 0: %v\n%s", hook, err, out)
	}
}

func gitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return p
}

func TestHookCommandQueuesWithoutEntireThenReplays(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo, home := t.TempDir(), t.TempDir()
	if out, err := exec.Command(gitPath(t), "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	runHookCommand(t, repo, home, HookUserPromptSubmit, `{"prompt":"one"}`)
	runHookCommand(t, repo, home, HookPostToolUse, `{"tool":"exec"}`)
	queue := filepath.Join(repo, ".entire", "tmp", "devin-pending")
	files, _ := filepath.Glob(filepath.Join(queue, "*"))
	if len(files) != 2 || !strings.HasSuffix(files[1], "."+HookUserPromptSubmit) && !strings.HasSuffix(files[0], "."+HookUserPromptSubmit) {
		t.Fatalf("payload not queued: %v", files)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("queued payload readable by others: %v", fi.Mode())
	}

	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The fake entire rejects post-tool-use and stop to cover failed replays
	// and a failing live hook.
	fake := "#!/bin/sh\nprintf '%s|' \"$*\" >>\"$LOG\"; cat >>\"$LOG\"; echo >>\"$LOG\"; [ \"$3\" = user-prompt-submit ]\n"
	for _, name := range []string{"entire", "entire-agent-devin"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runHookCommand(t, repo, home, HookStop, `{"stop":true}`)
	log, _ := os.ReadFile(filepath.Join(home, "log"))
	want := "hooks devin user-prompt-submit|{\"prompt\":\"one\"}\nhooks devin post-tool-use|{\"tool\":\"exec\"}\nhooks devin stop|{\"stop\":true}\n"
	if string(log) != want {
		t.Fatalf("entire calls:\n%s\nwant:\n%s", log, want)
	}
	if files, _ := filepath.Glob(filepath.Join(queue, "*")); len(files) != 1 || files[0] != filepath.Join(queue, "failed") {
		t.Fatalf("queue not drained: %v", files)
	}
	if failed, _ := filepath.Glob(filepath.Join(queue, "failed", "*."+HookPostToolUse)); len(failed) != 1 {
		t.Fatalf("rejected replay not kept: %v", failed)
	}
}

func TestHooksFileKeepsShellReadable(t *testing.T) {
	repo := t.TempDir()
	if _, err := InstallHooks(repo, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(HooksFile(repo))
	if strings.Contains(string(data), `\u003e`) || !strings.Contains(string(data), ">/dev/null") {
		t.Fatalf("hooks file escapes shell syntax:\n%s", data)
	}
}

func TestInstallHooksReplacesOlderWrapper(t *testing.T) {
	repo := t.TempDir()
	older := `PATH="$PATH:` + hookSearchPath() + `"; umask 077; q=x; if true; then entire hooks devin stop; else cat >"$q/1.stop"; fi; exit 0`
	cfg := `{"Stop":[{"matcher":"","hooks":[{"type":"command","command":` + jsonString(t, older) + `,"timeout":60}]}]}`
	if err := os.MkdirAll(filepath.Dir(HooksFile(repo)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HooksFile(repo), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHooks(repo, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(HooksFile(repo))
	if strings.Count(string(data), "q=x;") != 0 || strings.Count(string(data), entireHookPrefix+HookStop+";") != 1 {
		t.Fatalf("older wrapper not replaced:\n%s", data)
	}
}

func TestInstallHooksUpgradesLegacyCommands(t *testing.T) {
	repo := t.TempDir()
	legacy := `{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"entire hooks devin stop","timeout":60},{"type":"command","command":"echo entire hooks devin stop >>log"}]}]}`
	_ = os.MkdirAll(filepath.Dir(HooksFile(repo)), 0o755)
	if err := os.WriteFile(HooksFile(repo), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasEntireHooks(repo) {
		t.Fatal("legacy hooks must still be dispatch targets")
	}
	if ok, _ := AreHooksInstalled(repo); ok {
		t.Fatal("legacy hooks must be reported as needing an upgrade")
	}
	if n, err := InstallHooks(repo, false); err != nil || n != len(devinHookEvents) {
		t.Fatalf("upgrade: %d %v", n, err)
	}
	data, _ := os.ReadFile(HooksFile(repo))
	if strings.Count(string(data), entireHookPrefix+HookStop+";") != 1 || strings.Contains(string(data), `"entire hooks devin stop"`) {
		t.Fatalf("legacy hook not replaced:\n%s", data)
	}
	if err := UninstallHooks(repo); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(HooksFile(repo)); !strings.Contains(string(data), "echo entire hooks devin stop >>log") {
		t.Fatalf("user hook mentioning entire was removed:\n%s", data)
	}
}
