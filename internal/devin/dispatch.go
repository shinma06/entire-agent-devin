package devin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DispatchTargets lists the repositories a Devin plugin hook should be forwarded to:
// candidates that have Entire's Devin hooks installed. The plugin's shell command
// runs `entire hooks devin` itself so Entire resolves the long-lived Devin process,
// not this short-lived binary, as the session owner.
func DispatchTargets() []string {
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
	var out []string
	for _, c := range candidates {
		root := gitRoot(c)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if ok, _ := AreHooksInstalled(root); ok {
			out = append(out, root)
		}
	}
	return out
}

func gitRoot(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
