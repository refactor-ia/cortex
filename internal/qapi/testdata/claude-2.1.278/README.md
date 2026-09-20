# Claude Code 2.1.278 captured streams

These fixtures are real `claude -p --output-format stream-json` streams captured
on a maintainer machine on 2026-09-20. They exist so the `claude` backend
adapter can be tested offline against the shape the CLI actually emits, rather
than against a shape we guessed.

## CLI version

`claude_code_version` reported by the `system/init` event: `2.1.278`.

## Capture commands

`responses/stream-success.jsonl` — authenticated, ambient configuration
neutralized, single successful turn:

```sh
claude -p 'Reply with exactly: OK' \
  --output-format stream-json --verbose \
  --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --settings '{"hooks":{},"disableAllHooks":true}'
```

`responses/stream-unauthenticated.jsonl` — the same command with `HOME` pointed
at an empty directory, which removes authentication. Process exit code was `1`:

```sh
HOME=<empty-dir> claude -p 'Reply with exactly: OK' \
  --output-format stream-json --verbose \
  --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --settings '{"hooks":{},"disableAllHooks":true}'
```

A headless run is **not** isolated by default: a capture taken without
`--strict-mcp-config`, `--mcp-config` and `--settings` carried the operator's
session hooks and MCP server inventory into the stream. That contaminated
capture is deliberately not stored here. Isolating `HOME` removes ambient
configuration but also removes credentials, so the adapter must forward
credentials while neutralizing configuration — both live under the same root.

## Observed shapes

- Success: `system/init` -> `assistant` -> `rate_limit_event` -> `result`, with
  `result.is_error=false`, `result.result` carrying the terminal text, plus
  `num_turns` and `usage`.
- Unauthenticated: `system/init` -> `assistant` (`message.model="<synthetic>"`,
  text `Not logged in · Please run /login`, `error="authentication_failed"`) ->
  `result` with `is_error=true`. A typed failure, not a crash.

## What was scrubbed

Field names, event order and event structure are byte-faithful to the capture.
Only values that identify a machine, an operator or a session were replaced:

- `session_id` / `sessionID` -> `00000000-0000-4000-8000-000000000000`
- every `uuid` -> `00000000-0000-4000-8000-0000000000NN`, numbered in stream order
- `request_id` -> `req_00000000000000000000000000`
- message `id` -> `msg_00000000000000000000000000`
- `cwd` -> `/fixture/workspace`
- `memory_paths` values -> `/fixture/workspace/.fixture-memory/`
- `messaging_socket_path` -> `/fixture/run/agent.sock`
- ISO `timestamp` -> `2026-01-01T00:00:00.000Z`
- `permissionMode` and `output_style` -> `default`
- `slash_commands`, `skills`, `agents`, `plugins` -> `[]`, because the captured
  values were the operator's personal installation inventory, including absolute
  paths under their home directory

Re-capture rather than hand-editing these files when the stream contract changes.
