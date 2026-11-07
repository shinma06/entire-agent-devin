# Devin agent research

Observed from real Devin cloud sessions with a capture plugin.

## Hook payloads

- Delivered on stdin as JSON. Common keys: `hook_event_name`, `session_id` (`devin-<id>`), `prompt_id`.
- No `cwd`; the repository is found through `DEVIN_PROJECT_DIR` (also `CLAUDE_PROJECT_DIR`).
- `SessionStart`: `source` (`startup`, `resume`).
- `UserPromptSubmit`: `prompt` wrapped in `<received_chat_message ...>...</received_chat_message>`; the wrapper is stripped.
- `PreToolUse` / `PostToolUse`: `tool_name`, `tool_use_id`, `tool_input`, and (post) `tool_response`.
  `tool_input` is usually an object but sometimes a Python-repr string; `tool_response` is a Python-repr string
  such as `{'success': True, 'output': '...', 'error': None}`.
- `Stop`: `stop_hook_active`, `last_assistant_message` (often empty; user-facing text is in `message_user` tool calls).
- `SessionEnd` is not fired in cloud sessions, so checkpoints are created at `Stop` (TurnEnd).

## Hook config

Project hooks live in `.devin/hooks.v1.json` (Claude-style matcher groups). Plugin hooks live in a plugin's `hooks.json`.
Hooks are fail-open; changes load in new sessions.

## Transcript

Native format is JSONL (one `Record` per hook) because Entire slices transcripts by line offsets.
`compact-transcript` converts it to Entire Transcript Format (`user` / `assistant` lines with `text` and `tool_use` blocks).

## Resume

Devin sessions are resumed in the web app: `open https://app.devin.ai/sessions/<id>`.
