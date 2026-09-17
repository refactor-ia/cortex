# Cortex

**Cortex installs a curated set of agent capabilities into the AI coding runtimes you already use, and takes them back out cleanly.**

You point it at your machine. It detects which runtimes are present — [Pi](https://github.com/earendil-works/pi), OpenCode, Claude Code — writes its own skills and agents into each one, and stops there. Your configuration stays yours: Cortex owns only the files it created, writes them in a transaction that rolls back on failure, and `uninstall` removes exactly what it installed and nothing else.

It does not install those runtimes, replace them, or sit between you and them. When you are not running a Cortex command, nothing about your setup is different.

> **Status — the lifecycle works; the product is not finished.** This repository contains the target architecture and community foundation. `doctor`, `install`, `update` and `uninstall` are executable today, and you can run them right now. **Implementation is not complete:** the capability catalog is largely empty, full runtime-family parity is not claimed, and there is no certified release path. Releases are prereleases for evaluation.

## Install

Download a prerelease archive from [Releases](https://github.com/refactor-ia/cortex/releases), verify it, and put the binary on your `PATH`:

```bash
tar xzf cortex_v0.1.0-alpha.2_darwin_arm64.tar.gz   # or _linux_amd64
shasum -a 256 -c --ignore-missing SHA256SUMS
install -m 0755 cortex ~/.local/bin/cortex
```

Each archive carries `cortex`, `LICENSE`, and a `BUILDINFO.json` recording the exact source commit, tree, Go version and build flags it was produced from.

If you have Go installed, this works as well:

```bash
go install github.com/refactor-ia/cortex/cmd/cortex@latest
```

Building from source works too, and is the way to run unreleased changes:

```bash
git clone https://github.com/refactor-ia/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

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
operation=install status=completed touch=applied create=6 replace=0 remove=0 unchanged=0 preserve=0 warning=uncertified_admission runtimes=1 certification=not_certified
runtime=pi presence=present compatibility=compatible action=configure touch=applied
runtime=opencode presence=present compatibility=compatible action=configure touch=applied
runtime=claude-code presence=present compatibility=uncertified action=configure touch=applied
```

Running it again is a no-op, and reports that honestly as `create=0 unchanged=6`. `uninstall` removes only what Cortex owns:

```
$ cortex uninstall
runtime=pi uninstall=completed remove=2 absent=0 conflict=0
runtime=opencode uninstall=completed remove=2 absent=0 conflict=0
runtime=claude-code uninstall=completed remove=2 absent=0 conflict=0
```

Every command reports one line per runtime, in the same order, in `key=value` form meant to be read by a person and parsed by a script.

## How Cortex decides what to touch

| What Cortex finds | What it does |
| --- | --- |
| A runtime it recognises, at a version with verified evidence | Configures it, reported as `compatible` |
| A runtime it recognises, at any other identified version | Configures it and discloses `uncertified` — you are not pinned to versions we chose |
| A runtime recorded as known-incompatible | Skips that adapter only; the others still apply |
| A version it cannot identify | Refuses to write to that runtime. Cortex will not touch what it cannot recognise |
| A capability the runtime cannot represent honestly | Refuses rather than approximating |
| A file it does not own | Fails closed before mutating anything |

Writes are transactional and read back before success is reported; a failed transaction rolls back. The versions with verified end-to-end evidence are listed under [Runtime admission evidence](#runtime-admission-evidence) — that table records what was tested, and does not gate what you may install.

## What works today—and what does not

| Status | Scope |
| --- | --- |
| **Executable today** | Read-only `doctor`; transactional `install` and `update`; conservative `uninstall` of exact Cortex-owned state and artifacts. Synthetic three-runtime transaction parity covers this lifecycle. |
| **Implemented foundations** | Catalog schemas, loading, admission, and snapshots; rendering, projection, and artifact planning; and the runtime matrix. These are not yet an end-to-end product. |
| **Target only** | Full 11×3 runtime-family parity, release certification, the capability catalog, family packages, and agent prompts. |

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
