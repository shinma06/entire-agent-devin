package devin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Dispatch forwards a Devin plugin hook to `entire hooks devin <hook>` in every
// candidate repository that has Entire's Devin hooks installed. It never fails
// the Devin session: errors are reported on stderr only.
func Dispatch(hook string, stdin io.Reader, stderr io.Writer) {
	payload, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "entire-agent-devin: read hook payload: %v\n", err)
		return
	}
	for _, repo := range dispatchTargets() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "entire", "hooks", AgentName, hook)
		cmd.Dir = repo
		cmd.Stdin = bytes.NewReader(payload)
		cmd.Stdout = io.Discard
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "entire-agent-devin: %s in %s: %v\n", hook, repo, err)
		}
		cancel()
	}
}

func dispatchTargets() []string {
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
