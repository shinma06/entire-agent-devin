package devin

import (
	"encoding/json"
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
	runEntireEnable = func(string, io.Writer) error { enabled++; return nil }
	t.Cleanup(func() { runEntireEnable = orig })

	for _, r := range []string{"me/old", "me/ckpt"} {
		if err := Bootstrap(repo, r, io.Discard); err != nil {
			t.Fatalf("Bootstrap(%s): %v", r, err)
		}
	}
	out, err := exec.Command("git", "-C", repo, "config", "--get", "remote."+CheckpointRemote+".url").Output()
	if err != nil || strings.TrimSpace(string(out)) != "https://github.com/me/ckpt.git" {
		t.Fatalf("checkpoints remote = %q, %v", out, err)
	}
	var s map[string]any
	data, _ := os.ReadFile(filepath.Join(repo, ".entire", "settings.local.json"))
	if err := json.Unmarshal(data, &s); err != nil || s["external_agents"] != true || s["telemetry"] != false {
		t.Fatalf("settings = %s, %v", data, err)
	}
	if enabled != 2 {
		t.Fatalf("entire enable ran %d times, want 2", enabled)
	}
	if err := Bootstrap(repo, "bad", io.Discard); err == nil {
		t.Fatal("Bootstrap accepted a repo without owner")
	}
}
