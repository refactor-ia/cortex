# OpenCode 1.18.25 captured stream

`responses/stream-success.json` is a real `opencode run --format json` stream
captured on a maintainer machine on 2026-09-20, so the `opencode` backend
adapter can be tested offline against the shape the CLI actually emits.

## CLI version

`opencode` 1.18.25, reported by `opencode --version` at capture time. The stream
itself carries no version field.

## Capture command

```sh
opencode run --format json 'Reply with exactly: OK'
```

Despite `--format json`, the output is one JSON object per line, so the file is
JSON Lines and is parsed line by line.

## Observed shape

`step_start` -> `text` -> `step_finish`, with `step_finish.part.reason="stop"`
and a `step_finish.part.tokens` block (`total`, `input`, `output`, `reasoning`,
`cache`). The terminal text lives on the `text` event as `part.text`; there is no
single terminal result field the way Claude Code has one.

## What was scrubbed

Field names, event order and event structure are byte-faithful to the capture.
Only session-identifying values were replaced:

- `sessionID` -> `ses_00000000000000000000000000`
- part `id` -> `prt_00000000000000000000000000`
- `messageID` and the provider `metadata.openai.itemId` -> `msg_00000000000000000000000000`

Re-capture rather than hand-editing this file when the stream contract changes.
