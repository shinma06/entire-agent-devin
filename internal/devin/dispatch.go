package devin

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DispatchTargets lists the repositories a Devin plugin hook should be forwarded to:
// candidates that have Entire's Devin hooks installed. The plugin's shell command
// runs `entire hooks devin` itself so Entire resolves the long-lived Devin process,
// not this short-lived binary, as the session owner.
func DispatchTargets() []string { return dispatchTargets(false) }

// BootstrapDispatchTargets is DispatchTargets for the plugin hooks: candidates that opted into
// Entire (Devin hooks or a committed .entire/settings.json) and still need it are bootstrapped
// first, so `external_agents` is on in every repository a session works in.
func BootstrapDispatchTargets(out io.Writer) []string { return dispatchTargetsWith(true, out) }

func dispatchTargets(bootstrap bool) []string { return dispatchTargetsWith(bootstrap, io.Discard) }

func dispatchTargetsWith(bootstrap bool, out io.Writer) []string {
	var candidates []string
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if dir := os.Getenv("DEVIN_PROJECT_DIR"); dir != "" {
		candidates = append(candidates, dir)
	}
	globs := os.Getenv("ENTIRE_DEVIN_REPOS")
	if globs == "" {
		if home, err := os.UserHomeDir(); err == nil {
			globs = filepath.Join(home, "repos", "*")
		}
	}
	for _, g := range filepath.SplitList(globs) {
		matches, _ := filepath.Glob(g)
		candidates = append(candidates, matches...)
	}
	seen := map[string]bool{}
	var targets []string
	for _, c := range candidates {
		root := gitRoot(c)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if bootstrap && (hasEntireHooks(root) || exists(filepath.Join(root, ".entire", "settings.json"))) && NeedsBootstrap(root) {
			if err := Bootstrap(root, "", io.Discard); err != nil {
				fmt.Fprintf(out, "entire-agent-devin: bootstrap %s failed\n", root)
			}
		}
		if hasEntireHooks(root) {
			targets = append(targets, root)
		}
	}
	return targets
}

func gitRoot(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
