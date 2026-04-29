# mcp-bg-job

An MCP server that runs shell commands in the background and notifies Claude Code when they finish.

## Overview

Claude Code's MCP tools are synchronous — a tool call blocks until it returns. This server works around that by starting a process in the background, returning a job ID immediately, and sending a completion event over Claude Code Channels when the process exits.

**Transport:** stdin/stdout JSON-RPC (MCP stdio transport). No network, no daemon — Claude Code spawns the binary directly.

**Persistence:** Job state is stored as JSON files under `~/.claude-longjob-mcp/jobs/`. Logs are written to `~/.claude-longjob-mcp/logs/` with RFC3339 timestamps prepended to each line.

**Completion events:** When a job finishes, the server sends a `notifications/claude/channel` notification. Claude Code surfaces this as a `<channel>` element in the conversation. The `instruction` field passed at job start is embedded in the event so Claude knows what to do next.

## Architecture

```
Claude Code
  │  MCP stdio (JSON-RPC)
  ▼
mcp-bg-job (binary)
  ├── start_long_job  → spawns child process, returns job_id
  ├── get_job         → reads in-memory job state
  ├── list_jobs       → lists all known jobs
  ├── cancel_job      → sends SIGTERM to process group
  └── tail_job_log    → reads last N lines from log file

On job exit → notifications/claude/channel → Claude Code conversation
```

Key source files:

| File | Role |
|---|---|
| `main.go` | JSON-RPC dispatch, job lifecycle, process management |
| `notifier.go` | `Notifier` interface and `JobEvent` struct |
| `channel_notifier.go` | Sends `notifications/claude/channel` over stdout |
| `timestamp_writer.go` | Prepends RFC3339 timestamps to log lines |

## Development

```sh
go test ./...
go build .
```

No external dependencies — standard library only.

## Release

The release process is: **test → build → tag → publish → install**.

1. **Verify** all tests pass before tagging.
2. **Tag** with a semver version (`vX.Y.Z`). Increment patch for bug fixes, minor for new features, major for breaking changes.
3. **Push** the tag to the remote so the release is recorded in git history.
4. **Install** the new binary to wherever Claude Code is configured to find it (typically somewhere on `$PATH`). The MCP server process is spawned fresh by Claude Code, so replacing the binary takes effect on the next Claude Code restart — no separate restart step is needed for the binary itself.

The binary is self-contained (statically linked, no runtime deps), so installation is just a file copy.
