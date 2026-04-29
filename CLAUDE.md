# mcp-bg-job

An MCP server that runs shell commands in the background and notifies Claude Code when they finish.

## Overview

Claude Code's MCP tools are synchronous — a tool call blocks until it returns. This server works around that by starting a process in the background, returning a job ID immediately, and sending a completion event over Claude Code Channels when the process exits.

**Transport:** stdin/stdout JSON-RPC (MCP stdio transport). No network, no daemon — Claude Code spawns the binary directly.

**Persistence:** Job state is stored as JSON files under `~/.claude-longjob-mcp/jobs/`. Logs are written to `~/.claude-longjob-mcp/logs/` with RFC3339 timestamps prepended to each line.

**Completion events:** When a job finishes, the server sends a `notifications/claude/channel` notification. Claude Code surfaces this as a `<channel>` element in the conversation. The `instruction` field passed at job start is embedded in the event so Claude knows what to do next.

## Architecture

Claude Code spawns this binary over stdio and speaks JSON-RPC (MCP stdio transport). When a job is started, the server forks a child process and returns immediately. When the child exits, it fires a `notifications/claude/channel` event back over stdout — Claude Code surfaces this as a `<channel>` element in the active conversation.

Job state is persisted to disk so it survives server restarts. Logs are written with RFC3339 timestamps prepended to each line.

The codebase has no external dependencies — standard library only. Read the source directly; it's small enough to fit in one sitting.

## Development

```sh
go test ./...
go build .
```

## Release

```sh
# 1. Confirm tests are green
go test ./...

# 2. Tag with the next semver (patch / minor / major)
git tag vX.Y.Z
git push origin main --tags

# 3. Build and install the binary
go build -o ~/.local/bin/mcp-bg-job .
```

The binary is self-contained (no runtime deps). Claude Code spawns it fresh per session, so the new binary takes effect on the next Claude Code restart — no other steps needed.
