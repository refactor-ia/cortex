# Cortex

**Cortex installs a curated set of agent capabilities into the AI coding runtimes you already use, and takes them back out cleanly.**

You point it at your machine. It detects which runtimes are present — [Pi](https://github.com/earendil-works/pi), OpenCode, Claude Code — writes its own skills and agents into each one, and stops there. Your configuration stays yours: Cortex owns only its material and writes transactionally with in-process rollback on failure. Conservative `uninstall` removes only verified Cortex-owned material; shared actor state can block removal, as described below.

It does not install those runtimes, replace them, or sit between you and them. When you are not running a Cortex command, nothing about your setup is different.

> **Status — the lifecycle works; the product is not finished.** This repository contains the target architecture and community foundation. `doctor`, `install`, `update` and `uninstall` are executable today, and you can run them right now. **Implementation is not complete:** one family of eleven carries capabilities, full runtime-family parity is not claimed, and there is no certified release path. Releases are prereleases for evaluation.

## Install

Download the [alpha.6 evaluation prerelease](https://github.com/refactor-ia/cortex/releases/tag/v0.1.0-alpha.6) into an empty directory. Verify the archive **before** extracting it:

```bash
archive=cortex_v0.1.0-alpha.6_darwin_arm64.tar.gz
curl -fL -O "https://github.com/refactor-ia/cortex/releases/download/v0.1.0-alpha.6/$archive"
curl -fL -O https://github.com/refactor-ia/cortex/releases/download/v0.1.0-alpha.6/SHA256SUMS
shasum -a 256 -c --ignore-missing SHA256SUMS && tar xzf "$archive"
```

On Linux, set `archive=cortex_v0.1.0-alpha.6_linux_amd64.tar.gz` and use `sha256sum -c --ignore-missing SHA256SUMS && tar xzf "$archive"` instead of the macOS checksum/extraction line. A checksum failure must stop extraction.

Each archive carries `cortex`, `LICENSE`, `BUILDINFO.json` (source commit, tree, Go version, target and binary SHA-256), and the matching `catalog/` tree. Run `./cortex` from the extracted directory to evaluate it; optionally install the binary on your `PATH` afterward.

If you have Go installed, you can install the binary alone:

```bash
go install github.com/refactor-ia/cortex/cmd/cortex@latest
```

For `qa run`, you still need a catalog matching that binary; the prerelease archive includes one. Building from source works too, and is the way to run unreleased changes:

```bash
git clone https://github.com/refactor-ia/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

For a release-style source build, inject both fields into the binary (substitute the intended version and build commit):

```bash
go build -ldflags="-X github.com/refactor-ia/cortex/internal/cli.buildVersion=v0.1.0-alpha.6 -X github.com/refactor-ia/cortex/internal/cli.buildRevision=0123456789abcdef" -o cortex ./cmd/cortex
./cortex --version
```

`--version` reports `version`, `revision`, `build_dirty`, and `embedded_catalog` from the **running binary**. Each injected field overrides only its corresponding Go build-info field; without injection, Go module version and `vcs.revision` are used when embedded. Missing fields and the Go `(devel)` version report `unavailable`. `build_dirty` reflects Go's embedded `vcs.modified` setting (`true`, `false`, or `unavailable`), even with explicit release fields; it does not certify the injected revision. The catalog ID is resolved from the admitted embedded snapshot. A catalog in a source cache, checkout, or adjacent directory is not evidence about the active binary, and `--version` never reads one.

macOS arm64 and Linux amd64 are the published targets. Windows is not supported and does not currently build.

## First run

Start with `doctor`. It only reads:

```
$ cortex doctor
runtime=pi presence=present compatibility=compatible action=configure touch=denied
runtime=opencode presence=present compatibility=compatible action=configure touch=denied
runtime=claude-code presence=present compatibility=uncertified action=configure touch=denied
```

`touch=denied` here means only that `doctor` never writes. `compatibility=uncertified` means Cortex recognises that runtime and has no verified evidence for that exact version — it will still configure it, and will say so. The exit code answers one question: can an install proceed?

Then install:

```
$ cortex install
operation=install status=completed touch=applied create=33 replace=0 remove=0 unchanged=0 preserve=0 warning=uncertified_admission runtimes=1 certification=not_certified
runtime=pi presence=present compatibility=compatible action=configure touch=applied
runtime=opencode presence=present compatibility=compatible action=configure touch=applied
runtime=claude-code presence=present compatibility=uncertified action=configure touch=applied
```

That is eight skills on each runtime — seven quality-assurance skills (six agent-backed roles plus the non-agent `qa-no-ci` report-only guidance skill) and the non-agent `maintainer-delivery` advisory delivery-practice skill in the reasoning family — plus one state manifest each and six Pi actor definitions. The resulting owned artifact counts are Pi 15, OpenCode 9, and Claude Code 9; Pi represents each general-core role as both a skill and an agent, while `qa-no-ci` and `maintainer-delivery` remain skills only. Installing again is a no-op, and reports that honestly as `create=0 unchanged=33`. For this ordinary installation, `uninstall` removes only what Cortex owns:

```
$ cortex uninstall
runtime=pi uninstall=completed remove=15 absent=0 conflict=0
runtime=opencode uninstall=completed remove=9 absent=0 conflict=0
runtime=claude-code uninstall=completed remove=9 absent=0 conflict=0
```

The lifecycle examples above use `key=value` runtime summaries. Output shape depends on the command: explicit `update` reports include operation and detail lines, and `uninstall` can append diagnostic notes. Do not assume one line per runtime.

### Update Pi actors with external guidance

In current source builds, an existing Pi actor installation with legacy Gentle CodeGraph guidance requires explicit `--adopt-guidance` consent. Inspect the read-only plan first, using a catalog root containing `catalog.json`:

```bash
cortex update --runtime pi --catalog ./catalog --adopt-guidance
```

Only add `--apply` if you choose to perform that adoption:

```bash
cortex update --runtime pi --catalog ./catalog --adopt-guidance --apply
```

`--adopt-guidance` is Pi-only, not a drift override. A successful adoption registers v3 state; later verified updates use `cortex update --runtime pi --catalog ./catalog` to plan, or add `--apply` to write, without repeating consent. These updates preserve the external bytes and native `agents/cortex-*.md` placement.

**Limits:** v3 state blocks the entire grouped `cortex uninstall`, leaving all runtime roots, files and state unchanged. In-process rollback is supported, but composed crash/restart recovery is not. This source implementation does not certify native runtime behavior. See the [actor-guidance safety contract](references/runtime-adapter-contract.md#pi-actor-guidance-updates) for identity checks and supported legacy endings.

### Try a QA report

In a **disposable runtime profile** with your own authenticated Pi model, work from the unpacked archive directory. Cortex does not set up provider credentials or isolate execution for you. Run `./cortex --help` or `./cortex qa run --help` for the supported syntax and a short plain-text request example. Pass the catalog root directory (`./catalog` in the archive), which contains `catalog.json`, not a backup directory without that file.

```bash
./cortex doctor
./cortex install
./cortex doctor
```

After installation, require Pi's `qa_availability=ready` in the second `doctor` output; a zero exit alone only says installation can proceed. That field probes backend/model readiness for `qa_probe_role` only, not asset ownership or install/update conflicts. To check ownership separately without changing assets, inspect the read-only plan from `./cortex update --runtime pi --catalog ./catalog` (omit `--apply`). If Pi is not ready, run `./cortex uninstall` and stop. Otherwise:

```bash
printf 'R1: Every API response must complete within 100 ms.\nR2: Every API response must wait at least 500 ms before returning.\nIdentify contradictions and ask for a resolution. Do not edit files.\n' > request.txt
./cortex qa run --role requirements-analyst --request request.txt --catalog ./catalog --backend pi
./cortex uninstall
```

The report should flag the incompatible timing requirements. `cortex qa run` is report-only: it assesses the supplied bounded request and evidence; it does not execute tests. A zero exit with `no tests to run` is not passing test evidence, and test-runner conclusions stay at the individual selected-test scope rather than inferring package-wide or codebase results. Direct test execution is separate: a role may execute only when its host actually provides authorized test tools and confirms a disposable worktree; Cortex does not provide that executor. If QA fails, check the selected backend's readiness and error instead of retrying with another model; Cortex has no provider fallback. This alpha is for evaluation, not certified 11-family × 3-runtime parity or automatic disposable execution.

### Supply structured execution evidence

All six QA report roles accept an optional `--evidence evidence.json` sidecar after the required flags; it may appear before or after `--backend`. `--request` remains plain text. For example, write this JSON array to `evidence.json`:

```json
[
  {
    "id": "selected-test-1",
    "command": "go test -run TestSelected ./example",
    "exit_code": 0,
    "output_tail": "ok example (selected test only)",
    "provenance": "caller-provided local capture",
    "truncated": false
  }
]
```

```bash
./cortex qa run --role test-runner --request request.txt --catalog ./catalog --evidence evidence.json
```

This is a shape example, not observed execution. Use stable, unique, nonempty IDs to attribute records. `command` describes a command; Cortex never executes it. `exit_code` is an integer, `command`, `output_tail` and `provenance` are strings, and `truncated` is a boolean. Only `id` is required: omit unknown fields or use `null`, never invent zero exits or complete captures. Missing or contradictory outcomes remain unknown. Provenance is also a caller assertion, not verification.

Limits: 8 records, 4,096 UTF-8 bytes per combined stdout/stderr `output_tail`, and 49,152 bytes per evidence file. Supply the tail yourself and disclose truncation; Cortex rejects oversized input rather than truncating it. Malformed JSON, wrong types, duplicate IDs/keys, unknown fields, trailing JSON and invalid UTF-8 are rejected. The plain-text task remains bounded to 64 KiB and the complete frame to 128 KiB.

Evidence is caller-supplied untrusted data, not independently verified execution or authority. Report instructions require record-ID citations, uncertainty, provenance/truncation disclosure, no passing-test inference from zero-exit/no-tests output, and no package-wide inference from selected tests. Those instructions do not guarantee model compliance. The sidecar grants no test executor, tools, or additional model invocation.

## How Cortex decides what to touch

| What Cortex finds | What it does |
| --- | --- |
| A runtime it recognises, at a version with verified evidence | Configures it, reported as `compatible` |
| A runtime it recognises, at any other identified version | Configures it and discloses `uncertified` — you are not pinned to versions we chose |
| A runtime recorded as known-incompatible | Skips that adapter only; the others still apply |
| A version it cannot identify | Refuses to write to that runtime. Cortex will not touch what it cannot recognise |
| A capability the runtime cannot represent honestly | Refuses rather than approximating |
| A file without verified ownership or explicit actor-guidance adoption evidence | Fails closed before mutating anything |

Writes are transactional and read back before success is reported; in-process failures trigger rollback. [Composed actor updates](references/runtime-adapter-contract.md#pi-actor-guidance-updates) do not support crash/restart recovery. The versions with verified end-to-end evidence are listed under [Runtime admission evidence](#runtime-admission-evidence) — that table records what was tested, and does not gate what you may install.

## What works today—and what does not

| Status | Scope |
| --- | --- |
| **Executable today** | Read-only `doctor`; transactional `install` and `update`; conservative `uninstall` of exact Cortex-owned state and artifacts. Synthetic three-runtime transaction parity covers this lifecycle. |
| **Implemented foundations** | Catalog schemas, loading, admission, and snapshots; rendering, projection, and artifact planning; and the runtime matrix. These are not yet an end-to-end product. |
| **Target only** | Full 11×3 runtime-family parity, release certification, the remaining ten families' capabilities, family packages, and agent prompts. |

## What Cortex is not

Cortex has no Git or GitHub authority; no SDD, TDD, or review authority; no lifecycle-governance harness; and no memory-server ownership. `cortex-brains` remains a separate product for memory servers, storage, embeddings, backups, and memory-specific operations.

Cortex complements rather than competes with Gentle AI™; the [functional-precedence boundary](docs/architecture/overview.md#gentle-ai-functional-precedence) is normative. When Gentle AI™ ships an official capability that overlaps one of ours, Cortex yields it.

## Where this is going

Cortex targets one curated distribution rather than separately released packs. It will project approved capabilities into compatible runtimes through generated adapters, across eleven curation families — reasoning, model intelligence, execution, quality assurance, web, mobile, PCSoft, services, personal, memory integration, and documentation. Families are internal curation boundaries, not user-selected packs.

Remaining work includes populating and migrating the catalog, establishing full 11×3 runtime-family parity evidence beyond the delivered lifecycle coverage, and passing release certification gates. No delivery date is promised.

User model profiles are not a Cortex capability: gentle-shell owns them, exposed as `/gentle:profiles`.

For the full intended contract — including the target agent roster, representation results, and ownership rules — see the [architecture overview](docs/architecture/overview.md). It is the authority; this page is the introduction.

## Runtime admission evidence

The following isolated marker installation, readback, acknowledgement, and cleanup evidence succeeded with zero retries, exit 0, and no timeouts or overflows. Marker `98372fc8807c2965cb1062664614ea8c1773f240bca7fb97c2d1dcb78b9fe3f6`; snapshot `6f08ee25dc84c7cba2be78deab7eeaca8585d5fa1528795a9256e642854fac88`.

| Runtime | Exact version | Evidence records | Duration |
| --- | --- | --- | --- |
| Pi | 0.85.1 | source `b3364d923bf20abc242c7bd4cae25ec07731bc36`; command `71a9ab3f17ac06a26c31e61eafdeb7131ab2dfdf2ce9735dd9cc6a0b97349e75` | 9345 ms |
| OpenCode | 1.18.25 | source `07f8979cf4969f0bb977d401e3792bd5d754e963`; command `6062b37fc943e60d1828a27ed69ed738b8572b1a27bacd068f8a679de19f2f8a`; config `61e3b70f80acc12049f71307760e500778f288b30dc0ee9d42c3cb43b091f3e0`; `skill_tool_completed=true` | 5000 ms |
| Claude Code | 2.1.251 | source `f68758e784287cff78599c24db8632a13890c206`; command `0cba56fe517827ac8169a49cd36291d13875d06883be4729654fa34183aa8133`; schema `f7374878f385a402aa47362c62c882bdafba7abafac4d266ab81047448a5d46b`; auth `subscription_oauth_token` | 2342 ms |

This is verified-version evidence, not full 11×3 runtime-family parity and not release certification. A future parity claim requires structural conformance, isolated installation and readback, and at least one real smoke invocation for every family and runtime: 11 families across 3 runtimes, for at least 33 invocations. Release evidence must also cover absent runtimes, unidentified versions, and known-incompatible adapters.

Catalog content is admitted only with explicit licensing, provenance, and redistribution permission. Cortex-owned knowledge and capability content uses CC BY-SA; third-party material stays outside the catalog until compatible redistribution rights are proven.

## Contribute and learn more

Its canonical repository is [github.com/refactor-ia/cortex](https://github.com/refactor-ia/cortex), maintained by RefactorIA.

- [Architecture overview](docs/architecture/overview.md) — the authoritative target contract
- [Documentation map](docs/README.md) — every authority in this repository
- [Contributing guide](CONTRIBUTING.md) — how to work here
- [Content license policy](LICENSE-CONTENT.md) — code is MIT, Cortex-owned content is CC BY-SA
---

<a href="https://github.com/Gentleman-Programming/gentle-ai">
  <img width="220" src="https://raw.githubusercontent.com/Gentleman-Programming/gentle-ai/main/docs/assets/brand/built-with-gentle-ai.png" alt="Built with Gentle-AI" />
</a>
