# entire-agent-devin

[Entire](https://entire.io) external agent integration for [Devin](https://devin.ai).
It turns Devin sessions into Entire sessions and checkpoints (prompt, tool calls, modified files, summary).

Implements Entire external agent protocol v1 with the `hooks`, `transcript_analyzer` and `compact_transcript` capabilities.

## How it works

Devin has no transcript file, so the integration builds one from Devin hook payloads:

| Devin hook | Entire hook name | Entire event |
|---|---|---|
| `SessionStart` | `session-start` | SessionStart |
| `UserPromptSubmit` | `user-prompt-submit` | TurnStart |
| `PostToolUse` | `post-tool-use` | (transcript only) |
| `Stop` | `stop` | TurnEnd (checkpoint) |
| `PostCompaction` | `post-compaction` | Compaction |
| `SessionEnd` | `session-end` | SessionEnd (not fired by cloud Devin) |

Each hook appends one JSONL record to `.entire/tmp/devin/<session_id>.jsonl`.
Python-repr `tool_input` / `tool_response` strings are decoded to JSON.
The same hook delivered twice (plugin + project hooks) is recorded once.

## Install

```bash
go install github.com/shinma06/entire-agent-devin/cmd/entire-agent-devin@latest   # must end up on $PATH

cd your-repo
mkdir -p .entire && echo '{"external_agents": true}' > .entire/settings.local.json
entire enable --agent devin
```

`entire enable` writes `.devin/hooks.v1.json`, which runs `entire hooks devin <hook>`.

For sessions where project hooks are not loaded, install the Devin plugin in [`plugin/`](plugin/) as a personal plugin.
It runs `entire hooks devin <hook>` in every repository listed by `entire-agent-devin dispatch-targets`
(current dir, `$DEVIN_PROJECT_DIR`, and `$ENTIRE_DEVIN_REPOS` globs, default `~/repos/*`) that has Entire's Devin hooks installed.

## Checkpoint storage

Checkpoints contain the full Devin transcript, including tool output. For public repositories,
store them in a private repository:

```bash
entire configure --project --checkpoint-remote github:<owner>/<private-repo>
```

Keep `origin` pointing at the GitHub URL (or the `entire://` mirror). Entire only uses the
checkpoint remote when `origin` and the push remote belong to the checkpoint repository's owner;
otherwise it pushes transcripts to the push remote itself. When pushing through an `entire://`
mirror, mirror the checkpoint repository too (`entire repo mirror add /gh/<owner>/<private-repo>`).

## Development

```bash
go test ./...
go build -o bin/entire-agent-devin ./cmd/entire-agent-devin
go install github.com/entireio/external-agents-tests@latest
(cd /tmp && external-agents-tests verify "$OLDPWD/bin/entire-agent-devin")
```

See [AGENT.md](AGENT.md) for the Devin hook research behind the mapping.
