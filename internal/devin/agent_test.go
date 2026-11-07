package devin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	user := `{"PreToolUse":[{"matcher":"exec","hooks":[{"type":"command","command":"./check.sh","async":true}]}],"Stop":[{"matcher":"","x":1,"hooks":[{"type":"command","command":"./stop.sh"}]}]}`
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
	partial, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.Replace(string(partial), entireHookPrefix+HookStop, "./other.sh", 1)), 0o644)
	if ok, _ := AreHooksInstalled(repo); ok {
		t.Fatal("partial install reported as installed")
	}
	if n, _ := InstallHooks(repo, false); n != len(devinHookEvents) {
		t.Fatalf("partial install not repaired, got %d", n)
	}
	if err := UninstallHooks(repo); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"async": true`, `"x": 1`, "./stop.sh", "./other.sh"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("uninstall lost %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), entireHookPrefix) {
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

func TestParseHookConcurrentDuplicates(t *testing.T) {
	repo := t.TempDir()
	stop := loadPayloads(t, repo)["Stop"]
	events := make(chan *protocol.Event, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev, err := ParseHook(HookStop, strings.NewReader(stop), repo)
			if err != nil {
				t.Error(err)
			}
			events <- ev
		}()
	}
	wg.Wait()
	close(events)
	n := 0
	for ev := range events {
		if ev != nil {
			n++
		}
	}
	recs, _, _ := RecordsFrom(SessionFile(SessionDir(repo), "devin-0123456789abcdef"), 0)
	if n != 1 || len(recs) != 1 {
		t.Fatalf("events=%d records=%d, want 1 and 1", n, len(recs))
	}
}

func TestModifiedFilesSkipsFailedTools(t *testing.T) {
	recs := []Record{
		{Kind: KindTool, ToolName: "edit", ToolInput: []byte(`{"file_path":"a.go"}`), ToolResponse: []byte(`{"success":false,"output":"","error":"no match"}`)},
		{Kind: KindTool, ToolName: "edit", ToolInput: []byte(`{"file_path":"b.go"}`), ToolResponse: []byte(`{"success":true,"output":"ok","error":null}`)},
	}
	if got := ModifiedFiles(recs, "/repo"); len(got) != 1 || got[0] != "b.go" {
		t.Fatalf("got %v", got)
	}
}

func TestCleanPromptQuotedArtifact(t *testing.T) {
	in := "<received_chat_message from=\"x\">\n<quoted_artifact kind=\"message\">\n<caption>Yes, merge it.</caption>\n<content>\nMay I merge &lt;x&gt;?\nOr not?\n</content>\n</quoted_artifact>\n</received_chat_message>"
	want := "Yes, merge it.\n> May I merge <x>?\n> Or not?"
	if got := cleanPrompt(in); got != want {
		t.Fatalf("cleanPrompt = %q, want %q", got, want)
	}
}

func TestFirstToolCallOpensTurn(t *testing.T) {
	repo := t.TempDir()
	parse := func(hook, body string) *protocol.Event {
		t.Helper()
		ev, err := ParseHook(hook, strings.NewReader(body), repo)
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	tool := func(id string) string {
		return `{"session_id":"devin-x","prompt_id":"p1","tool_name":"exec","tool_use_id":"` + id + `","tool_input":{},"tool_response":"{}"}`
	}
	if ev := parse(HookPostToolUse, tool("t1")); ev == nil || ev.Type != protocol.EventTurnStart {
		t.Fatalf("first tool call = %+v, want TurnStart", ev)
	}
	if ev := parse(HookPostToolUse, tool("t2")); ev != nil {
		t.Fatalf("second tool call = %+v, want nil", ev)
	}
	if ev := parse(HookUserPromptSubmit, `{"session_id":"devin-x","prompt_id":"p2","prompt":"hi"}`); ev == nil || ev.Type != protocol.EventTurnStart {
		t.Fatalf("prompt = %+v", ev)
	}
	if ev := parse(HookPostToolUse, `{"session_id":"devin-x","prompt_id":"p2","tool_name":"exec","tool_use_id":"t3","tool_input":{},"tool_response":"{}"}`); ev != nil {
		t.Fatalf("tool call after prompt = %+v, want nil", ev)
	}
}
