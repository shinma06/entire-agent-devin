package devin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	HookSessionStart     = "session-start"
	HookUserPromptSubmit = "user-prompt-submit"
	HookPostToolUse      = "post-tool-use"
	HookStop             = "stop"
	HookPostCompaction   = "post-compaction"
	HookSessionEnd       = "session-end"

	entireHookPrefix = "entire hooks devin "
)

// devinHookEvents maps Devin hook event names to Entire hook names.
var devinHookEvents = []struct{ Event, Hook string }{
	{"SessionStart", HookSessionStart},
	{"UserPromptSubmit", HookUserPromptSubmit},
	{"PostToolUse", HookPostToolUse},
	{"Stop", HookStop},
	{"PostCompaction", HookPostCompaction},
	{"SessionEnd", HookSessionEnd},
}

func hookNames() []string {
	out := make([]string, 0, len(devinHookEvents))
	for _, e := range devinHookEvents {
		out = append(out, e.Hook)
	}
	return out
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// HooksFile is the project-level Devin hooks config written by install-hooks.
func HooksFile(repoRoot string) string {
	return filepath.Join(repoRoot, ".devin", "hooks.v1.json")
}

func readHooksConfig(path string) (map[string]json.RawMessage, error) {
	cfg := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func writeHooksConfig(path string, cfg map[string]json.RawMessage) error {
	if len(cfg) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

type group = map[string]json.RawMessage

// stripEntireHooks removes Entire commands from one event's matcher groups,
// keeping every other group, hook and field as written.
func stripEntireHooks(event string, raw json.RawMessage) ([]group, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var groups []group
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, false, fmt.Errorf("%s: unsupported hook config: %w", event, err)
	}
	removed := false
	kept := groups[:0]
	for _, g := range groups {
		var hooks []group
		if json.Unmarshal(g["hooks"], &hooks) != nil {
			kept = append(kept, g)
			continue
		}
		rest := hooks[:0]
		for _, h := range hooks {
			if strings.HasPrefix(hookCommandOf(h), entireHookPrefix) {
				removed = true
				continue
			}
			rest = append(rest, h)
		}
		if len(rest) == len(hooks) {
			kept = append(kept, g)
			continue
		}
		if len(rest) == 0 {
			continue
		}
		data, err := json.Marshal(rest)
		if err != nil {
			return nil, false, err
		}
		g["hooks"] = data
		kept = append(kept, g)
	}
	return kept, removed, nil
}

func hookCommandOf(h group) string {
	var cmd string
	_ = json.Unmarshal(h["command"], &cmd)
	return cmd
}

func hasHookCommand(raw json.RawMessage, command string) bool {
	var groups []group
	if json.Unmarshal(raw, &groups) != nil {
		return false
	}
	for _, g := range groups {
		var hooks []group
		_ = json.Unmarshal(g["hooks"], &hooks)
		for _, h := range hooks {
			if hookCommandOf(h) == command {
				return true
			}
		}
	}
	return false
}

func InstallHooks(repoRoot string, force bool) (int, error) {
	path := HooksFile(repoRoot)
	cfg, err := readHooksConfig(path)
	if err != nil {
		return 0, err
	}
	if !force && hooksInstalled(cfg) {
		return 0, nil
	}
	for _, e := range devinHookEvents {
		groups, _, err := stripEntireHooks(e.Event, cfg[e.Event])
		if err != nil {
			return 0, err
		}
		entire, err := json.Marshal(map[string]any{
			"matcher": "",
			"hooks":   []hookCommand{{Type: "command", Command: entireHookPrefix + e.Hook, Timeout: 60}},
		})
		if err != nil {
			return 0, err
		}
		var g group
		if err := json.Unmarshal(entire, &g); err != nil {
			return 0, err
		}
		data, err := json.Marshal(append(groups, g))
		if err != nil {
			return 0, err
		}
		cfg[e.Event] = data
	}
	return len(devinHookEvents), writeHooksConfig(path, cfg)
}

func UninstallHooks(repoRoot string) error {
	path := HooksFile(repoRoot)
	cfg, err := readHooksConfig(path)
	if err != nil {
		return err
	}
	for event, raw := range cfg {
		groups, removed, err := stripEntireHooks(event, raw)
		if err != nil || !removed {
			continue
		}
		if len(groups) == 0 {
			delete(cfg, event)
			continue
		}
		data, err := json.Marshal(groups)
		if err != nil {
			return err
		}
		cfg[event] = data
	}
	return writeHooksConfig(path, cfg)
}

func AreHooksInstalled(repoRoot string) (bool, error) {
	cfg, err := readHooksConfig(HooksFile(repoRoot))
	if err != nil {
		return false, err
	}
	return hooksInstalled(cfg), nil
}

// hooksInstalled reports whether every Entire hook is present.
func hooksInstalled(cfg map[string]json.RawMessage) bool {
	for _, e := range devinHookEvents {
		if !hasHookCommand(cfg[e.Event], entireHookPrefix+e.Hook) {
			return false
		}
	}
	return true
}
