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
	want := "bootstrap|\nhooks devin user-prompt-submit|{\"prompt\":\"one\"}\nhooks devin post-tool-use|{\"tool\":\"exec\"}\nhooks devin stop|{\"stop\":true}\n"
	if string(log) != want {
		t.Fatalf("entire calls:\n%s\nwant:\n%s", log, want)
	}
	if files, _ := filepath.Glob(filepath.Join(queue, "*")); len(files) != 1 || files[0] != filepath.Join(queue, "failed") {
		t.Fatalf("queue not drained: %v", files)
	}
	if failed, _ := filepath.Glob(filepath.Join(queue, "failed", "*."+HookPostToolUse)); len(failed) != 1 {
		t.Fatalf("rejected replay not kept: %v", failed)
	}
	if failed, _ := filepath.Glob(filepath.Join(queue, "failed", "*."+HookStop)); len(failed) != 1 {
		t.Fatalf("rejected live payload not kept: %v", failed)
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
	if strings.Count(string(data), "q=x;") != 0 || strings.Count(string(data), entireHookPrefix+HookStop+" <") != 1 {
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
	if strings.Count(string(data), entireHookPrefix+HookStop+" <") != 1 || strings.Contains(string(data), `"entire hooks devin stop"`) {
		t.Fatalf("legacy hook not replaced:\n%s", data)
	}
	if err := UninstallHooks(repo); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(HooksFile(repo)); !strings.Contains(string(data), "echo entire hooks devin stop >>log") {
		t.Fatalf("user hook mentioning entire was removed:\n%s", data)
	}
}

func TestHookCommandUsesDevinProjectDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo, home := t.TempDir(), t.TempDir()
	if out, err := exec.Command(gitPath(t), "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cmd := exec.Command("sh", "-c", hookCommand(HookSessionStart))
	cmd.Dir = home
	cmd.Env = []string{"PATH=" + filepath.Dir(gitPath(t)) + ":/usr/bin:/bin", "HOME=" + home, "DEVIN_PROJECT_DIR=" + repo}
	cmd.Stdin = strings.NewReader(`{"source":"startup"}`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook command must exit 0: %v\n%s", err, out)
	}
	if files, _ := filepath.Glob(filepath.Join(repo, ".entire", "tmp", "devin-pending", "*."+HookSessionStart)); len(files) != 1 {
		t.Fatalf("payload not queued in DEVIN_PROJECT_DIR: %v", files)
	}
}

func TestHookCommandBootstrapsWithCommittedCheckpointRepo(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo, home := t.TempDir(), t.TempDir()
	if out, err := exec.Command(gitPath(t), "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	committed := "{\n  \"strategy_options\": {\n    \"checkpoint_remote\": {\n      \"provider\": \"github\",\n      \"repo\": \"me/ckpt\"\n    }\n  }\n}\n"
	failed := filepath.Join(repo, ".entire", "tmp", "devin-pending", "failed")
	if err := os.MkdirAll(failed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.json"), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(failed, "1-1.session-start"), []byte(`{"source":"startup"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksFile := filepath.Join(repo, ".devin", "hooks.v1.json")
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksFile, []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An older entire-agent-devin that needs --checkpoint-repo and appends its own hooks.
	agent := "#!/bin/sh\necho \"$*\" >>\"$LOG\"; echo older >>.devin/hooks.v1.json; [ \"$2\" = --checkpoint-repo ] && [ -n \"$3\" ] && echo '{\"checkpoint_push_remote\":\"checkpoints\"}' >.entire/settings.local.json\n"
	entire := "#!/bin/sh\nprintf '%s|' \"$*\" >>\"$LOG\"; cat >>\"$LOG\"; echo >>\"$LOG\"\n"
	for name, body := range map[string]string{"entire-agent-devin": agent, "entire": entire} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	runHookCommand(t, repo, home, HookUserPromptSubmit, `{"prompt":"two"}`)
	runHookCommand(t, repo, home, HookStop, `{"stop":true}`)
	log, _ := os.ReadFile(filepath.Join(home, "log"))
	want := "bootstrap --checkpoint-repo me/ckpt\nhooks devin session-start|{\"source\":\"startup\"}\nhooks devin user-prompt-submit|{\"prompt\":\"two\"}\nhooks devin stop|{\"stop\":true}\n"
	if string(log) != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", log, want)
	}
	if files, _ := filepath.Glob(filepath.Join(failed, "*")); len(files) != 0 {
		t.Fatalf("failed payloads not retried: %v", files)
	}
	if b, _ := os.ReadFile(hooksFile); string(b) != "committed\n" {
		t.Fatalf("hooks file changed by bootstrap: %q", b)
	}
	if files, _ := filepath.Glob(filepath.Join(repo, ".entire", "tmp", "devin-pending", ".*")); len(files) != 0 {
		t.Fatalf("leftover temp files: %v", files)
	}
}

func TestHookCommandBootstrapPassesOnlyPlainGitHubCheckpointRepos(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	for name, tc := range map[string]struct{ remote, want string }{
		"github":         {`{"provider":"github","repo":"me/ckpt"}`, "bootstrap --checkpoint-repo me/ckpt"},
		"repo first":     {`{"repo":"me/ckpt", "provider": "github"}`, "bootstrap --checkpoint-repo me/ckpt"},
		"other provider": {`{"provider":"gitlab","repo":"me/ckpt"}`, "bootstrap"},
		"github prefix":  {`{"provider":"githubx","repo":"me/ckpt"}`, "bootstrap"},
		"escaped":        {`{"provider":"github","repo":"me\/ckpt"}`, "bootstrap"},
		"not owner/repo": {`{"provider":"github","repo":"a/b/c"}`, "bootstrap"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			if out, err := exec.Command(gitPath(t), "init", "-q", repo).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			committed := `{"enabled":true,"strategy_options":{"checkpoint_remote":` + tc.remote + `}}`
			if err := os.MkdirAll(filepath.Join(repo, ".entire"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.json"), []byte(committed), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"entire-agent-devin": "#!/bin/sh\necho \"$*\" >>\"$LOG\"\n", "entire": "#!/bin/sh\ncat >/dev/null\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runHookCommand(t, repo, home, HookStop, `{"stop":true}`)
			if log, _ := os.ReadFile(filepath.Join(home, "log")); string(log) != tc.want+"\n" {
				t.Fatalf("calls %q, want %q", log, tc.want)
			}
		})
	}
}

func TestHookCommandSkipsBootstrapForLocalOnlyClones(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	repo, home := t.TempDir(), t.TempDir()
	if out, err := exec.Command(gitPath(t), "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".entire"), 0o700); err != nil {
		t.Fatal(err)
	}
	local := "{\n  \"external_agents\": true,\n  \"strategy_options\": {\n    \"push_sessions\": false\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(repo, ".entire", "settings.local.json"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"entire-agent-devin": "#!/bin/sh\necho \"$*\" >>\"$LOG\"\n", "entire": "#!/bin/sh\ncat >/dev/null; echo \"$*\" >>\"$LOG\"\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runHookCommand(t, repo, home, HookStop, `{"stop":true}`)
	if log, _ := os.ReadFile(filepath.Join(home, "log")); string(log) != "hooks devin stop\n" {
		t.Fatalf("calls %q", log)
	}
}
