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
Devin runs hooks with a minimal PATH (no shell profile), so the hook command also searches
`~/.local/bin`, `~/go/bin`, `~/bin`, `/opt/homebrew/bin` and `/usr/local/bin`, and always exits 0.
If `entire` or `entire-agent-devin` is still missing, the payload is queued in
`.entire/tmp/devin-pending/` (git-ignored, owner-only) and replayed by the next hook that finds both;
replays Entire rejects are kept in `.entire/tmp/devin-pending/failed/`.
Re-run `entire enable --agent devin` (or `bootstrap`) to upgrade hooks written by older versions.

### Devin VMs (Linux, macOS, Windows)

Devin hooks run under bash on every platform (Git Bash on Windows), so the same hook command is used everywhere;
only installation differs. Run once per VM, in the repository:

```bash
scripts/devin-setup.sh --checkpoint-repo owner/entire-checkpoints        # Linux, macOS
```

```powershell
scripts\devin-setup.ps1 --checkpoint-repo owner/entire-checkpoints       # Windows (PowerShell)
```

They install `entire` (official installer, `~/.local/bin`), Go if missing (`~/go-sdk`) and
`entire-agent-devin` (`~/go/bin`), then run `bootstrap`. Environment blueprints only run on the platform
they were built for, so macOS and Windows sessions need this step (or a blueprint for that platform).

Starting sessions:

- Specify the repository the work is for, so the session is recorded there (`DEVIN_PROJECT_DIR`) and that
  repository's blueprint runs. Sessions without one still run the project hooks of repositories already on the VM,
  and are recorded in those that have Entire enabled.
- Repositories need `external_agents` enabled (done by `bootstrap`); otherwise Entire answers `unknown agent "devin"`.
- Hooks load at session start, so after setup the records start with the current session's next hook,
  plus anything queued before.

For sessions where project hooks are not loaded, install the Devin plugin in [`plugin/`](plugin/) as a personal plugin.
It runs `entire hooks devin <hook>` in every repository listed by `entire-agent-devin dispatch-targets`
(current dir, `$DEVIN_PROJECT_DIR`, and `$ENTIRE_DEVIN_REPOS` globs, default `~/repos/*`) that has Entire's Devin hooks installed.

## Checkpoint storage

Checkpoints contain the full Devin transcript, including tool output. For public repositories,
store them in a private repository. In a fresh clone (for example from a Devin blueprint):

```bash
go install github.com/shinma06/entire-agent-devin/cmd/entire-agent-devin@latest
entire-agent-devin bootstrap --checkpoint-repo <owner>/<private-repo>
```

`bootstrap` adds a `checkpoints` remote, sets `external_agents` in `.entire/settings.local.json`,
and runs `entire enable --agent devin --checkpoint-push-remote checkpoints`. Pinning the remote is
required in Devin clones: their `origin` is a git proxy URL whose owner Entire cannot match to the
checkpoint repository, so without it Entire pushes transcripts to `origin`. The plugin's `Stop` hook
uploads new checkpoints to the `checkpoints` remote, so no Entire login is needed.

If `core.hooksPath` points at hooks committed to the repository (e.g. `.githooks`), `bootstrap`
redirects it to `.git/entire-hooks`, whose wrappers run the committed hooks, so `entire enable` never
rewrites tracked files. Files Entire generates are added to `.git/info/exclude`. Untracked settings
are per worktree: run `bootstrap` again in each new `git worktree`.

## Development

```bash
go test ./...
go build -o bin/entire-agent-devin ./cmd/entire-agent-devin
go install github.com/entireio/external-agents-tests@latest
(cd /tmp && external-agents-tests verify "$OLDPWD/bin/entire-agent-devin")
```

See [AGENT.md](AGENT.md) for the Devin hook research behind the mapping.
