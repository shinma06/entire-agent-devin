package devin

import (
	"bytes"
	"encoding/json"
	"errors"
)

const compactOutputLimit = 4000

type compactLine struct {
	V          int    `json:"v"`
	Agent      string `json:"agent"`
	CLIVersion string `json:"cli_version"`
	Type       string `json:"type"`
	TS         string `json:"ts,omitempty"`
	ID         string `json:"id,omitempty"`
	Content    any    `json:"content"`
}

type userBlock struct {
	Text string `json:"text"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolBlock struct {
	Type   string      `json:"type"`
	ID     string      `json:"id,omitempty"`
	Name   string      `json:"name"`
	Input  any         `json:"input"`
	Result *toolResult `json:"result,omitempty"`
}

type toolResult struct {
	Output string `json:"output"`
	Status string `json:"status"`
}

// compactTranscript converts the native transcript into Entire Transcript Format.
func compactTranscript(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range parseRecords(data) {
		line := compactLine{V: 1, Agent: AgentName, TS: r.TS}
		switch {
		case r.Kind == KindUser && r.Prompt != "":
			line.Type = "user"
			line.ID = r.PromptID
			line.Content = []userBlock{{Text: r.Prompt}}
		case assistantText(r) != "":
			line.Type = "assistant"
			line.ID = r.ToolUseID
			line.Content = []textBlock{{Type: "text", Text: assistantText(r)}}
		case r.Kind == KindTool:
			line.Type = "assistant"
			line.ID = r.ToolUseID
			var input any
			_ = json.Unmarshal(r.ToolInput, &input)
			line.Content = []toolBlock{{Type: "tool_use", ID: r.ToolUseID, Name: r.ToolName, Input: input, Result: compactResult(r.ToolResponse)}}
		default:
			continue
		}
		if err := enc.Encode(line); err != nil {
			return nil, err
		}
	}
	if buf.Len() == 0 {
		return nil, errors.New("transcript has no user, assistant or tool records")
	}
	return buf.Bytes(), nil
}

func compactResult(raw json.RawMessage) *toolResult {
	if len(raw) == 0 {
		return nil
	}
	res := &toolResult{Status: "success"}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		if ok, isBool := m["success"].(bool); isBool && !ok {
			res.Status = "error"
		}
		if e, ok := m["error"].(string); ok && e != "" {
			res.Status = "error"
			res.Output = e
		}
		if o, ok := m["output"].(string); ok && res.Output == "" {
			res.Output = o
		}
	} else {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			res.Output = s
		} else {
			res.Output = string(raw)
		}
	}
	if r := []rune(res.Output); len(r) > compactOutputLimit {
		res.Output = string(r[:compactOutputLimit]) + "…"
	}
	return res
}
