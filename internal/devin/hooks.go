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

type matcherGroup struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
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

// stripEntireHooks removes Entire commands from one event's matcher groups.
func stripEntireHooks(raw json.RawMessage) ([]matcherGroup, bool) {
	var groups []matcherGroup
	if json.Unmarshal(raw, &groups) != nil {
		return nil, false
	}
	removed := false
	kept := groups[:0]
	for _, g := range groups {
		hooks := g.Hooks[:0]
		for _, h := range g.Hooks {
			if strings.HasPrefix(h.Command, entireHookPrefix) {
				removed = true
				continue
			}
			hooks = append(hooks, h)
		}
		if len(hooks) > 0 {
			g.Hooks = hooks
			kept = append(kept, g)
		}
	}
	return kept, removed
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
		groups, _ := stripEntireHooks(cfg[e.Event])
		groups = append(groups, matcherGroup{Hooks: []hookCommand{{Type: "command", Command: entireHookPrefix + e.Hook, Timeout: 60}}})
		data, err := json.Marshal(groups)
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
		groups, removed := stripEntireHooks(raw)
		if !removed {
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

func hooksInstalled(cfg map[string]json.RawMessage) bool {
	for _, raw := range cfg {
		if _, removed := stripEntireHooks(raw); removed {
			return true
		}
	}
	return false
}
