package protocol

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func ReadJSON(r io.Reader, v any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode stdin: %w", err)
	}
	return nil
}

// Flags parses "--name value" style arguments into a map of the requested names.
func Flags(args []string, names ...string) (map[string]*string, error) {
	fs := flag.NewFlagSet("entire-agent-devin", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := make(map[string]*string, len(names))
	for _, n := range names {
		out[n] = fs.String(n, "", "")
	}
	fs.Bool("force", false, "")
	fs.Bool("local-dev", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return out, nil
}

// RepoRoot returns ENTIRE_REPO_ROOT, falling back to the working directory.
func RepoRoot() string {
	if root := os.Getenv("ENTIRE_REPO_ROOT"); root != "" {
		return root
	}
	wd, _ := os.Getwd()
	return wd
}
