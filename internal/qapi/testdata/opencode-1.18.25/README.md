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

## `responses/stream-error.json`

The typed failure stream, captured on 2026-09-20 from the same build with:

```sh
echo 'Reply with exactly: OK' | opencode run --format json --pure -m nonexistent/model
```

One `error` event on stdout and process exit 1. OpenCode reports a provider,
credential or model failure through this single envelope — `error.name` plus an
opaque `error.data.ref` — and does not distinguish "not authenticated" from any
other provider failure the way Claude Code's `terminal_reason` does. An
unauthenticated capture could not be produced on the capture machine: unlike
Claude Code, isolating `HOME` (and `XDG_DATA_HOME`/`XDG_CONFIG_HOME`) did not
de-authenticate OpenCode, so the same envelope was captured from a provider
failure that does reproduce. A run with a deliberately invalid
`OPENAI_API_KEY`/`ANTHROPIC_API_KEY` produced a byte-identical envelope apart
from its `ref`, which is the evidence that this is the shape an unauthenticated
run takes too.

## Isolation observed on this build

`opencode run` is not isolated by default. The default run loaded the
operator's external plugins: their `[skill-registry] skipping refresh` lines
appeared on stderr and the run reported 22516 input tokens for a one-word
prompt. The same run with `--pure` emitted nothing on stderr and reported
21599, so roughly 900 tokens of operator plugin context were reaching the
model. `--pure` is therefore load-bearing and is part of the adapter's argv.

One gap remains and is recorded rather than worked around: OpenCode still loads
the project context files of its working directory and no flag disables them.
`--pure` in the Cortex repository reported 22575 input tokens against 21599 in
an empty directory, the difference being the repository's own `AGENTS.md`.

## What was scrubbed

Field names, event order and event structure are byte-faithful to the capture.
Only session-identifying values were replaced:

- `sessionID` -> `ses_00000000000000000000000000`
- part `id` -> `prt_00000000000000000000000000`
- `messageID` and the provider `metadata.openai.itemId` -> `msg_00000000000000000000000000`
- `error.data.ref` -> `err_00000000`

Re-capture rather than hand-editing this file when the stream contract changes.
