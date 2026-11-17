package devin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// Install locations of entire and entire-agent-devin (install.sh/install.ps1,
// go install, Homebrew). Devin runs hooks and setup with a PATH that skips the
// user's shell profile, so these are searched explicitly.
var (
	toolHomeDirs = []string{".local/bin", "go/bin", "bin"}
	toolAbsDirs  = []string{"/opt/homebrew/bin", "/usr/local/bin"}
)

func hookSearchPath() string {
	dirs := make([]string, 0, len(toolHomeDirs)+len(toolAbsDirs))
	for _, d := range toolHomeDirs {
		dirs = append(dirs, "$HOME/"+d)
	}
	return strings.Join(append(dirs, toolAbsDirs...), ":")
}

// AddToolDirsToPath appends the install locations to this process's PATH, so
// commands it starts (entire, and entire-agent-devin started by entire) resolve
// the same way the hook command does.
func AddToolDirsToPath() {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if self, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(self))
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range toolHomeDirs {
			dirs = append(dirs, filepath.Join(home, filepath.FromSlash(d)))
		}
	}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, toolAbsDirs...)
	}
	_ = os.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// hookCommand returns the POSIX shell command installed for hook. It never fails
// the hook: when entire or entire-agent-devin cannot be found the payload is
// queued under .entire/tmp/devin-pending (git-ignored, owner-only) and replayed,
// oldest first, by the next hook that can reach both; a payload entire rejects
// is kept in devin-pending/failed. It runs in the DEVIN_PROJECT_DIR repository.
// An unbootstrapped clone is bootstrapped first, passing the committed
// GitHub checkpoint_remote explicitly so older entire-agent-devin binaries work too;
// on success the failed payloads are retried. The hooks file is kept as it was, since
// bootstrap's `entire enable` lets an older binary append its own wrappers to it.
func hookCommand(hook string) string {
	return hookWrapperPrefix() +
		`cd "${DEVIN_PROJECT_DIR:-.}" 2>/dev/null; r="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"; cd "$r"; q="$r/.entire/tmp/devin-pending"; ` +
		`if command -v entire >/dev/null 2>&1 && command -v entire-agent-devin >/dev/null 2>&1; then ` +
		`grep -qs '"checkpoint_push_remote"' "$r/.entire/settings.local.json" || { ` +
		`o="$(tr -d ' \t\r\n' <"$r/.entire/settings.json" 2>/dev/null | sed -n 's/.*"checkpoint_remote":{\([^}]*\)}.*/\1/p')"; c=; ` +
		`case ",$o," in *',"provider":"github",'*) c="$(printf %s ",$o," | sed -n 's/.*,"repo":"\([^"\\/]*\/[^"\\/]*\)",.*/\1/p')";; esac; ` +
		`h="$r/.devin/hooks.v1.json"; mkdir -p "$q" && cp -p "$h" "$q/.hooks.$$" 2>/dev/null; ` +
		`entire-agent-devin bootstrap ${c:+--checkpoint-repo "$c"} </dev/null >/dev/null 2>&1 && ` +
		`for f in "$q"/failed/*; do [ -f "$f" ] && mv -f "$f" "$q/"; done; [ -f "$q/.hooks.$$" ] && mv -f "$q/.hooks.$$" "$h"; }; ` +
		`for f in "$q"/*; do [ -f "$f" ] || continue; if entire hooks devin "${f##*.}" <"$f" >/dev/null 2>&1; then rm -f "$f"; else mkdir -p "$q/failed" && mv -f "$f" "$q/failed/"; fi; done; ` +
		`mkdir -p "$q" && cat >"$q/.$$" && if ` + entireHookPrefix + hook + ` <"$q/.$$"; then rm -f "$q/.$$"; else mkdir -p "$q/failed" && mv -f "$q/.$$" "$q/failed/$(date +%s)-$$.` + hook + `"; fi; ` +
		`else mkdir -p "$q" && cat >"$q/.$$" && mv -f "$q/.$$" "$q/$(date +%s)-$(printf %06d "$(ls "$q" | wc -l)")-$$.` + hook + `"; fi; exit 0`
}

func hookWrapperPrefix() string {
	return `PATH="$PATH:` + hookSearchPath() + `"; umask 077; `
}

// isEntireHookCommand matches only commands install-hooks writes (the bare
// legacy form, or any version of the queueing wrapper), so user hooks that
// merely mention entire are left alone.
func isEntireHookCommand(cmd string) bool {
	wrapper := hookWrapperPrefix()
	for _, e := range devinHookEvents {
		if cmd == entireHookPrefix+e.Hook ||
			(strings.HasPrefix(cmd, wrapper) && strings.HasSuffix(cmd, "."+e.Hook+`"; fi; exit 0`)) {
			return true
		}
	}
	return false
}

func hookNames() []string {
	out := make([]string, 0, len(devinHookEvents))
	for _, e := range devinHookEvents {
		out = append(out, e.Hook)
	}
	return out
}

type hookEntry struct {
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
	compact, err := marshalJSON(cfg)
	if err != nil {
		return err
	}
	var data bytes.Buffer
	if err := json.Indent(&data, compact, "", "  "); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data.WriteByte('\n')
	return os.WriteFile(path, data.Bytes(), 0o644)
}

type group = map[string]json.RawMessage

// marshalJSON is json.Marshal without HTML escaping, so shell redirections in
// hook commands stay readable in the hooks file.
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

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
			if isEntireHookCommand(hookCommandOf(h)) {
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
		data, err := marshalJSON(rest)
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
		entire, err := marshalJSON(map[string]any{
			"matcher": "",
			"hooks":   []hookEntry{{Type: "command", Command: hookCommand(e.Hook), Timeout: 60}},
		})
		if err != nil {
			return 0, err
		}
		var g group
		if err := json.Unmarshal(entire, &g); err != nil {
			return 0, err
		}
		data, err := marshalJSON(append(groups, g))
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
		data, err := marshalJSON(groups)
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
		if !hasHookCommand(cfg[e.Event], hookCommand(e.Hook)) {
			return false
		}
	}
	return true
}

// hasEntireHooks reports whether repoRoot has any Entire hook, including ones
// written by earlier versions, so plugin dispatch keeps reaching repositories
// whose committed hooks have not been upgraded yet.
func hasEntireHooks(repoRoot string) bool {
	cfg, err := readHooksConfig(HooksFile(repoRoot))
	if err != nil {
		return false
	}
	for _, raw := range cfg {
		var groups []group
		if json.Unmarshal(raw, &groups) != nil {
			continue
		}
		for _, g := range groups {
			var hooks []group
			_ = json.Unmarshal(g["hooks"], &hooks)
			for _, h := range hooks {
				if isEntireHookCommand(hookCommandOf(h)) {
					return true
				}
			}
		}
	}
	return false
}
