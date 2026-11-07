// Package devin implements the Entire external agent protocol for Devin.
package devin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/shinma06/entire-agent-devin/internal/protocol"
)

const AgentName = "devin"

func Info() protocol.InfoResponse {
	return protocol.InfoResponse{
		ProtocolVersion: protocol.ProtocolVersion,
		Name:            AgentName,
		Type:            "Devin",
		Description:     "Devin - Cognition's autonomous AI software engineer (via plugin hooks)",
		IsPreview:       true,
		ProtectedDirs:   []string{".devin"},
		HookNames:       hookNames(),
		Capabilities: protocol.Capabilities{
			Hooks:              true,
			TranscriptAnalyzer: true,
			CompactTranscript:  true,
		},
	}
}

func Detect(repoRoot string) bool {
	if os.Getenv("DEVIN_PROJECT_DIR") != "" {
		return true
	}
	if st, err := os.Stat(filepath.Join(repoRoot, ".devin")); err == nil && st.IsDir() {
		return true
	}
	return false
}

func SessionDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".entire", "tmp", "devin")
}

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func SessionFile(dir, sessionID string) string {
	id := unsafeID.ReplaceAllString(sessionID, "_")
	if id == "" || id == "." || id == ".." {
		id = "unknown"
	}
	return filepath.Join(dir, id+".jsonl")
}

// payload is the raw Devin hook stdin.
type payload struct {
	HookEventName        string          `json:"hook_event_name"`
	SessionID            string          `json:"session_id"`
	PromptID             string          `json:"prompt_id"`
	Source               string          `json:"source"`
	Prompt               string          `json:"prompt"`
	ToolName             string          `json:"tool_name"`
	ToolUseID            string          `json:"tool_use_id"`
	ToolInput            json.RawMessage `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	Model                string          `json:"model"`
}

var now = func() time.Time { return time.Now().UTC() }

// ParseHook appends the hook to the session transcript and returns the lifecycle event, or nil.
func ParseHook(hook string, stdin io.Reader, repoRoot string) (*protocol.Event, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var p payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode devin hook payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, errors.New("devin hook payload has no session_id")
	}
	ts := now().Format(time.RFC3339Nano)
	ref := SessionFile(SessionDir(repoRoot), p.SessionID)
	rec := Record{V: 1, TS: ts, SessionID: p.SessionID, PromptID: p.PromptID, Source: p.Source}
	ev := &protocol.Event{SessionID: p.SessionID, SessionRef: ref, Timestamp: ts, Model: p.Model}

	switch hook {
	case HookSessionStart:
		rec.Kind, ev.Type = KindSessionStart, protocol.EventSessionStart
	case HookUserPromptSubmit:
		rec.Kind, ev.Type = KindUser, protocol.EventTurnStart
		rec.Prompt = cleanPrompt(p.Prompt)
		ev.Prompt = rec.Prompt
	case HookPostToolUse:
		rec.Kind, ev = KindTool, nil
		rec.ToolName, rec.ToolUseID = p.ToolName, p.ToolUseID
		rec.ToolInput, rec.ToolResponse = normalizeJSON(p.ToolInput), normalizeJSON(p.ToolResponse)
	case HookStop:
		rec.Kind, ev.Type = KindAssistant, protocol.EventTurnEnd
		rec.Message = p.LastAssistantMessage
	case HookPostCompaction:
		rec.Kind, ev.Type = KindCompaction, protocol.EventCompaction
	case HookSessionEnd:
		rec.Kind, ev.Type = KindSessionEnd, protocol.EventSessionEnd
	default:
		return nil, nil
	}

	added, err := recordOnce(ref, hook+"|"+p.PromptID+"|"+p.ToolUseID+"|"+p.Source, rec)
	if err != nil || !added {
		return nil, err
	}
	return ev, nil
}

// recordOnce appends rec unless key was already recorded. The same Devin hook can
// reach Entire twice (plugin + project hooks), possibly concurrently, so the
// check, the append and marking the key happen under one file lock.
func recordOnce(ref, key string, rec Record) (bool, error) {
	base := strings.TrimSuffix(ref, ".jsonl")
	if err := os.MkdirAll(filepath.Dir(ref), 0o750); err != nil {
		return false, err
	}
	lock, err := os.OpenFile(base+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	unlock, err := lockFile(lock)
	if err != nil {
		return false, err
	}
	defer unlock()

	seenPath := base + ".seen"
	if data, err := os.ReadFile(seenPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line == key {
				return false, nil
			}
		}
	}
	if err := appendRecord(ref, rec); err != nil {
		return false, err
	}
	f, err := os.OpenFile(seenPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return true, err
	}
	defer f.Close()
	_, err = f.WriteString(key + "\n")
	return true, err
}

func appendRecord(ref string, rec Record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ref), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(ref, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func ReadSession(in protocol.HookInput, repoRoot string) (protocol.AgentSession, error) {
	ref := in.SessionRef
	if ref == "" {
		ref = SessionFile(SessionDir(repoRoot), in.SessionID)
	}
	s := protocol.AgentSession{
		SessionID: in.SessionID, AgentName: AgentName, RepoPath: repoRoot, SessionRef: ref,
		ModifiedFiles: []string{}, NewFiles: []string{}, DeletedFiles: []string{},
	}
	data, err := os.ReadFile(ref)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.NativeData = data
	records := parseRecords(data)
	if len(records) > 0 {
		s.StartTime = records[0].TS
	}
	s.ModifiedFiles = modifiedFiles(records, repoRoot)
	return s, nil
}

func WriteSession(s protocol.AgentSession) error {
	if s.SessionRef == "" {
		return errors.New("write-session: session_ref is required")
	}
	if len(s.NativeData) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.SessionRef), 0o750); err != nil {
		return err
	}
	return os.WriteFile(s.SessionRef, s.NativeData, 0o600)
}

// ChunkTranscript splits on line boundaries so every chunk stays valid JSONL where possible.
func ChunkTranscript(data []byte, maxSize int) [][]byte {
	if maxSize <= 0 || len(data) <= maxSize {
		return [][]byte{data}
	}
	var chunks [][]byte
	var cur []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		for len(line) > maxSize {
			if len(cur) > 0 {
				chunks, cur = append(chunks, cur), nil
			}
			chunks, line = append(chunks, line[:maxSize]), line[maxSize:]
		}
		if len(cur)+len(line) > maxSize {
			chunks, cur = append(chunks, cur), nil
		}
		cur = append(cur, line...)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func ResumeCommand(sessionID string) string {
	return "open https://app.devin.ai/sessions/" + strings.TrimPrefix(sessionID, "devin-")
}

func TranscriptPosition(path string) (int, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(st.Size()), nil
}

// RecordsFrom reads records starting at a byte offset and returns the end position.
func RecordsFrom(path string, offset int) ([]Record, int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if offset < 0 || offset > len(data) {
		offset = 0
	}
	return parseRecords(data[offset:]), len(data), nil
}

func ModifiedFiles(records []Record, repoRoot string) []string {
	return modifiedFiles(records, repoRoot)
}
func Prompts(records []Record) []string             { return prompts(records) }
func Summary(records []Record) string               { return summary(records) }
func CompactTranscript(data []byte) ([]byte, error) { return compactTranscript(data) }
