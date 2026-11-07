package devin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Record is one line of the native Devin transcript this integration maintains.
type Record struct {
	V            int             `json:"v"`
	TS           string          `json:"ts"`
	Kind         string          `json:"kind"`
	SessionID    string          `json:"session_id"`
	PromptID     string          `json:"prompt_id,omitempty"`
	Source       string          `json:"source,omitempty"`
	Prompt       string          `json:"prompt,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	Message      string          `json:"message,omitempty"`
}

const (
	KindSessionStart = "session_start"
	KindUser         = "user"
	KindTool         = "tool"
	KindAssistant    = "assistant"
	KindCompaction   = "compaction"
	KindSessionEnd   = "session_end"
)

const messageTool = "message_user"

var chatWrapper = regexp.MustCompile(`(?s)^\s*<received_chat_message[^>]*>(.*)</received_chat_message>\s*$`)

func cleanPrompt(p string) string {
	if m := chatWrapper.FindStringSubmatch(p); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(p)
}

// normalizeJSON turns a Devin payload value into JSON, decoding Python-repr strings when possible.
func normalizeJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return raw
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return raw
	}
	if json.Valid([]byte(trimmed)) && (trimmed[0] == '{' || trimmed[0] == '[') {
		return json.RawMessage(trimmed)
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if v, err := parsePythonLiteral(trimmed); err == nil {
			if out, err := json.Marshal(v); err == nil {
				return out
			}
		}
	}
	return raw
}

func parseRecords(data []byte) []Record {
	var out []Record
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if json.Unmarshal(line, &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func inputMap(raw json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

var writeToolHints = []string{"edit", "write", "create", "replace", "delete", "remove", "move", "rename", "notebook", "patch"}
var pathKeys = []string{"file_path", "path", "filePath", "target_file", "notebook_path", "file", "new_path"}

func isWriteTool(name string) bool {
	n := strings.ToLower(name)
	for _, h := range writeToolHints {
		if strings.Contains(n, h) {
			return true
		}
	}
	return false
}

// modifiedFiles lists repo-relative paths touched by file-writing tools, in first-seen order.
func modifiedFiles(records []Record, repoRoot string) []string {
	seen := map[string]bool{}
	files := []string{}
	for _, r := range records {
		if r.Kind != KindTool || !isWriteTool(r.ToolName) || toolFailed(r.ToolResponse) {
			continue
		}
		m := inputMap(r.ToolInput)
		for _, k := range pathKeys {
			p, ok := m[k].(string)
			if !ok || p == "" {
				continue
			}
			if rel := repoRelative(p, repoRoot); rel != "" && !seen[rel] {
				seen[rel] = true
				files = append(files, rel)
			}
		}
	}
	return files
}

// toolFailed reports whether a Devin tool_response ({success, output, error}) signals failure.
func toolFailed(raw json.RawMessage) bool {
	m := inputMap(raw)
	if ok, isBool := m["success"].(bool); isBool && !ok {
		return true
	}
	e, _ := m["error"].(string)
	return e != ""
}

func repoRelative(p, repoRoot string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(filepath.Clean(p))
	}
	if repoRoot == "" {
		return ""
	}
	rel, err := filepath.Rel(repoRoot, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func prompts(records []Record) []string {
	out := []string{}
	for _, r := range records {
		if r.Kind == KindUser && r.Prompt != "" {
			out = append(out, r.Prompt)
		}
	}
	return out
}

// assistantText returns the user-facing text of a record: Stop messages and message_user calls.
func assistantText(r Record) string {
	switch {
	case r.Kind == KindAssistant:
		return r.Message
	case r.Kind == KindTool && r.ToolName == messageTool:
		if s, ok := inputMap(r.ToolInput)["message"].(string); ok {
			return s
		}
	}
	return ""
}

func summary(records []Record) string {
	for i := len(records) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(assistantText(records[i])); t != "" {
			return t
		}
	}
	return ""
}
