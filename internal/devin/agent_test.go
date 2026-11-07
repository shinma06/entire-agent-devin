package devin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shinma06/entire-agent-devin/internal/protocol"
)

func loadPayloads(t *testing.T, repo string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("testdata/payloads.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range raw {
		out[k] = strings.ReplaceAll(string(v), "/REPO", repo)
	}
	return out
}

func TestParseHookLifecycle(t *testing.T) {
	repo := t.TempDir()
	p := loadPayloads(t, repo)
	steps := []struct {
		hook, payload string
		want          int
	}{
		{HookSessionStart, p["SessionStart"], protocol.EventSessionStart},
		{HookUserPromptSubmit, p["UserPromptSubmit"], protocol.EventTurnStart},
		{HookPostToolUse, p["PostToolUseReprResponse"], 0},
		{HookPostToolUse, p["PostToolUseEdit"], 0},
		{HookPostToolUse, p["PostToolUseReprInput"], 0},
		{HookStop, p["Stop"], protocol.EventTurnEnd},
	}
	for _, s := range steps {
		ev, err := ParseHook(s.hook, strings.NewReader(s.payload), repo)
		if err != nil {
			t.Fatalf("%s: %v", s.hook, err)
		}
		if s.want == 0 {
			if ev != nil {
				t.Fatalf("%s: want nil event, got %+v", s.hook, ev)
			}
			continue
		}
		if ev == nil || ev.Type != s.want || ev.SessionID != "devin-0123456789abcdef" {
			t.Fatalf("%s: got %+v", s.hook, ev)
		}
		if s.hook == HookUserPromptSubmit && ev.Prompt != "Fix the login bug" {
			t.Fatalf("prompt not cleaned: %q", ev.Prompt)
		}
	}

	// A duplicate delivery of the same hook is ignored.
	if ev, err := ParseHook(HookStop, strings.NewReader(p["Stop"]), repo); err != nil || ev != nil {
		t.Fatalf("duplicate stop: ev=%+v err=%v", ev, err)
	}

	ref := SessionFile(SessionDir(repo), "devin-0123456789abcdef")
	recs, _, err := RecordsFrom(ref, 0)
	if err != nil || len(recs) != 6 {
		t.Fatalf("records: %d %v", len(recs), err)
	}
	var resp map[string]any
	if err := json.Unmarshal(recs[2].ToolResponse, &resp); err != nil || resp["output"] != "a.txt\nb's file" {
		t.Fatalf("repr tool_response not decoded: %s", recs[2].ToolResponse)
	}
	if got := ModifiedFiles(recs, repo); len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("modified files: %v", got)
	}
	if got := Prompts(recs); len(got) != 1 || got[0] != "Fix the login bug" {
		t.Fatalf("prompts: %v", got)
	}
	if got := Summary(recs); got != "Done: fixed the bug" {
		t.Fatalf("summary: %q", got)
	}

	data, _ := os.ReadFile(ref)
	compact, err := CompactTranscript(data)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(compact), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("compact lines = %d:\n%s", len(lines), compact)
	}
	if !bytes.Contains(lines[0], []byte(`"type":"user"`)) || !bytes.Contains(lines[3], []byte(`Done: fixed the bug`)) {
		t.Fatalf("unexpected compact transcript:\n%s", compact)
	}
}

func TestHooksInstallPreservesUserHooks(t *testing.T) {
	repo := t.TempDir()
	path := HooksFile(repo)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	user := `{"PreToolUse":[{"matcher":"exec","hooks":[{"type":"command","command":"./check.sh"}]}]}`
	if err := os.WriteFile(path, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := InstallHooks(repo, false); err != nil || n != len(devinHookEvents) {
		t.Fatalf("install: %d %v", n, err)
	}
	if n, _ := InstallHooks(repo, false); n != 0 {
		t.Fatalf("second install should be a no-op, got %d", n)
	}
	if ok, _ := AreHooksInstalled(repo); !ok {
		t.Fatal("hooks not reported installed")
	}
	if err := UninstallHooks(repo); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "./check.sh") || strings.Contains(string(data), entireHookPrefix) {
		t.Fatalf("uninstall left wrong content: %s", data)
	}
}

func TestParsePythonLiteral(t *testing.T) {
	v, err := parsePythonLiteral(`{'a': [1, -2.5, True, None], "b": 'it\'s \u00e9\n', 'c': (1,)}`)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(v)
	if string(got) != `{"a":[1,-2.5,true,null],"b":"it's é\n","c":[1]}` {
		t.Fatalf("got %s", got)
	}
}

func TestChunkTranscriptRoundTrip(t *testing.T) {
	data := []byte("{\"a\":1}\n{\"b\":22222222}\n{\"c\":3}\n")
	chunks := ChunkTranscript(data, 10)
	if len(chunks) < 2 || string(bytes.Join(chunks, nil)) != string(data) {
		t.Fatalf("bad chunks: %q", chunks)
	}
}
