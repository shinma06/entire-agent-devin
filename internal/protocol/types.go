// Package protocol holds the wire types of the Entire external agent protocol (version 1).
package protocol

import "encoding/json"

const ProtocolVersion = 1

const (
	EventSessionStart = 1
	EventTurnStart    = 2
	EventTurnEnd      = 3
	EventCompaction   = 4
	EventSessionEnd   = 5
)

type Capabilities struct {
	Hooks              bool `json:"hooks"`
	TranscriptAnalyzer bool `json:"transcript_analyzer"`
	TranscriptPreparer bool `json:"transcript_preparer"`
	TokenCalculator    bool `json:"token_calculator"`
	CompactTranscript  bool `json:"compact_transcript"`
	TextGenerator      bool `json:"text_generator"`
	HookResponseWriter bool `json:"hook_response_writer"`
	SubagentAware      bool `json:"subagent_aware_extractor"`
}

type InfoResponse struct {
	ProtocolVersion int          `json:"protocol_version"`
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	Description     string       `json:"description"`
	IsPreview       bool         `json:"is_preview"`
	ProtectedDirs   []string     `json:"protected_dirs"`
	HookNames       []string     `json:"hook_names"`
	Capabilities    Capabilities `json:"capabilities"`
	CallerEnvVars   []string     `json:"caller_env_vars,omitempty"`
}

type HookInput struct {
	HookType   string          `json:"hook_type"`
	SessionID  string          `json:"session_id"`
	SessionRef string          `json:"session_ref"`
	Timestamp  string          `json:"timestamp"`
	UserPrompt string          `json:"user_prompt,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	RawData    map[string]any  `json:"raw_data,omitempty"`
}

type AgentSession struct {
	SessionID     string   `json:"session_id"`
	AgentName     string   `json:"agent_name"`
	RepoPath      string   `json:"repo_path"`
	SessionRef    string   `json:"session_ref"`
	StartTime     string   `json:"start_time"`
	NativeData    []byte   `json:"native_data"`
	ModifiedFiles []string `json:"modified_files"`
	NewFiles      []string `json:"new_files"`
	DeletedFiles  []string `json:"deleted_files"`
}

type Event struct {
	Type       int               `json:"type"`
	SessionID  string            `json:"session_id"`
	SessionRef string            `json:"session_ref,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Model      string            `json:"model,omitempty"`
	Timestamp  string            `json:"timestamp,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type Chunks struct {
	Chunks [][]byte `json:"chunks"`
}

type CompactTranscriptResponse struct {
	Transcript string `json:"transcript"`
}
