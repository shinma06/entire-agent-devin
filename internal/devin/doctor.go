package devin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Doctor reports, without reading payloads or transcripts, why Devin sessions may not reach
// Entire: missing tools, unbootstrapped clones, stale or duplicated hooks, unsafe checkpoint
// destinations, and queued payloads. It returns the number of problems found.
func Doctor(out io.Writer) int {
	AddToolDirsToPath()
	problems := 0
	report := func(ok bool, format string, args ...any) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			problems++
		}
		fmt.Fprintf(out, "%s "+format+"\n", append([]any{mark}, args...)...)
	}
	for _, tool := range []string{"entire", "entire-agent-devin"} {
		path, err := exec.LookPath(tool)
		report(err == nil, "%s: %s", tool, orMissing(path))
	}
	roots := map[string]bool{}
	var repos []string
	for _, c := range append([]string{os.Getenv("DEVIN_PROJECT_DIR"), "."}, DispatchTargets()...) {
		if c == "" {
			continue
		}
		if root := gitRoot(c); root != "" && !roots[root] {
			roots[root] = true
			repos = append(repos, root)
		}
	}
	if len(repos) == 0 {
		fmt.Fprintln(out, "-    no git repository found (set DEVIN_PROJECT_DIR or ENTIRE_DEVIN_REPOS)")
	}
	for _, root := range repos {
		fmt.Fprintf(out, "\n%s\n", root)
		doctorRepo(root, report)
	}
	return problems
}

func doctorRepo(root string, count func(bool, string, ...any)) {
	cfg, err := readHooksConfig(HooksFile(root))
	switch {
	case err != nil:
		count(false, "hooks: %s unreadable", HooksFile(root))
	case hooksInstalled(cfg):
		count(true, "hooks: current wrapper for every event")
	case hasEntireHooks(root):
		count(false, "hooks: outdated Entire hooks (run `entire-agent-devin install-hooks`)")
	default:
		count(false, "hooks: Entire hooks not installed (run `entire-agent-devin bootstrap`)")
	}
	for event, raw := range cfg {
		if n := entireHookGroups(raw); n > 1 {
			count(false, "hooks: %s has %d Entire hook groups", event, n)
		}
	}
	settings, _ := readLocalSettings(root)
	pushRemote := settings.StrategyOptions.CheckpointPushRemote
	switch {
	case NeedsBootstrap(root):
		count(false, "settings.local.json: external_agents or a safe checkpoint destination missing (run `entire-agent-devin bootstrap`)")
	case pushRemote == "":
		count(true, "settings.local.json: external_agents on, checkpoints kept local")
	default:
		if _, err := gitOut(root, "remote", "get-url", pushRemote); err != nil {
			count(false, "settings.local.json: checkpoint remote %q missing (run `entire-agent-devin bootstrap --checkpoint-repo <owner/repo>`)", pushRemote)
		} else {
			count(true, "settings.local.json: external_agents on, checkpoints pushed to the %q remote", pushRemote)
		}
	}
	q := filepath.Join(root, ".entire", "tmp", "devin-pending")
	pending, failed := queued(q), queued(filepath.Join(q, "failed"))
	count(pending == 0, "queue: %d pending payloads", pending)
	count(failed == 0, "queue: %d payloads rejected by Entire (replayed after a successful bootstrap)", failed)
}

func entireHookGroups(raw json.RawMessage) int {
	var groups []group
	if json.Unmarshal(raw, &groups) != nil {
		return 0
	}
	n := 0
	for _, g := range groups {
		var hooks []group
		_ = json.Unmarshal(g["hooks"], &hooks)
		for _, h := range hooks {
			if isEntireHookCommand(hookCommandOf(h)) {
				n++
				break
			}
		}
	}
	return n
}

func queued(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

func orMissing(s string) string {
	if s == "" {
		return "not found on PATH or in " + hookSearchPath()
	}
	return s
}
