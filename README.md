# Cortex

Cortex puts a curated set of agent capabilities into the AI coding runtimes you already use. Today that means eight capabilities on Pi, OpenCode, and Claude Code: six quality-assurance roles, one report-only QA guide, and one delivery-practice skill.

It writes only its own files, transactionally, and removes exactly what it wrote on uninstall. It never installs or replaces a runtime, never touches your configuration, and does nothing at all when you are not running a Cortex command.

> **Status — the lifecycle works; the product is not finished.** This repository contains the target architecture and community foundation. `doctor`, `install`, `update` and `uninstall` are executable today. **Implementation is not complete:** one family of eleven carries capabilities, full runtime-family parity is not claimed, and there is no certified release path. Releases are prereleases for evaluation.

## What you get

| Capability | What it does |
| --- | --- |
| `requirements-analyst` | Assesses requirements for ambiguity, completeness, consistency, and testability. |
| `test-designer` | Produces test conditions and coverage rationale for a bounded evaluation. |
| `test-runner` | Assesses supplied test evidence at individual selected-test scope; executes only with authorized tools and a confirmed disposable worktree. |
| `exploratory-tester` | Investigates behavior and records reproducible observations. |
| `adversarial-tester` | Challenges assumptions, boundaries, and failure behavior. |
| `evidence-auditor` | Evaluates evidence sufficiency, attribution, and uncertainty. |
| `qa-no-ci` | A report-only guide for running QA when a project has no CI. |
| `maintainer-delivery` | An advisory checklist for taking an issue to merge; the operator decides and runs every step. |

On Pi, the six QA roles are also registered as agents, not just skills.

## Start in two minutes

Download the [latest evaluation prerelease](https://github.com/refactor-ia/cortex/releases) into an empty directory and verify the archive before extracting it (the exact `curl` + `shasum -c` commands are in each release's notes):

```bash
tar xzf cortex_v0.1.0-alpha.8_darwin_arm64.tar.gz
```

Then walk the lifecycle:

```bash
./cortex doctor     # reads only: what runtimes exist and what would happen
./cortex install    # writes Cortex's files into present runtimes
./cortex uninstall  # removes exactly what install wrote
```

`doctor` exit code 0 means an install can proceed. The install output names what was created; running install again is an honest no-op. If you have Go, `go install github.com/refactor-ia/cortex/cmd/cortex@latest` installs the binary alone.

### Run a QA report

From the unpacked archive directory, with your own authenticated model route:

```bash
printf 'R1: Every API response must complete within 100 ms.\nR2: Every API response must wait at least 500 ms before returning.\nIdentify the contradiction; do not edit files.\n' > request.txt
./cortex qa run --role requirements-analyst --request request.txt --catalog ./catalog --backend pi
```

The report should flag the two incompatible timing requirements. QA runs support Pi, Claude Code, and OpenCode headless backends (`--backend pi|claude|opencode`), and accept a bounded execution-evidence sidecar (`--evidence evidence.json`). It is report-only: it assesses the request and evidence you supply; it does not execute tests. Run it from a disposable directory. Cortex sets up no credentials, isolates no execution, and never falls back to another model.

## What Cortex does and refuses to do

| What it finds | What it does |
| --- | --- |
| A runtime it recognises, at a version with verified evidence | Configures it |
| A recognised runtime at another identified version | Configures it and discloses the admission is `uncertified` |
| A runtime recorded as known-incompatible | Skips that adapter only and reports the skip |
| A version it cannot identify | Refuses to write to that runtime |
| A capability the runtime cannot represent honestly | Refuses rather than approximating |
| A file without verified ownership or explicit adoption evidence | Fails closed before mutating anything |

Cortex has no Git or GitHub authority; no SDD, TDD, or review authority; no lifecycle-governance harness; and no memory-server ownership. It complements Gentle AI™: when Gentle AI™ ships an official capability that overlaps one of ours, Cortex yields it.

## Status

| Status | Scope |
| --- | --- |
| **Executable today** | Read-only `doctor`; transactional `install` and `update`; conservative `uninstall`. |
| **Implemented foundations** | Catalog schemas, loading, admission, and snapshots; rendering, projection, and artifact planning; the runtime matrix. Not yet an end-to-end product. |
| **Target only** | Full 11-family × 3-runtime parity, release certification, the remaining ten families' capabilities. |

Where it is going: one curated distribution projecting approved capabilities into compatible runtimes through generated adapters, across eleven curation families — reasoning, model intelligence, execution, quality assurance, web, mobile, PCSoft, services, personal, memory integration, and documentation. Families are internal curation boundaries, not user-selected packs. No delivery date is promised. User model profiles are not a Cortex capability: gentle-shell owns them.

## Reference

- [Architecture overview](docs/architecture/overview.md) — the authoritative target contract; this page is the introduction
- [Verification and release evidence](docs/verification.md) — archive hashes, runtime admission evidence, how to report results
- [Documentation map](docs/README.md) — every authority in this repository
- [Contributing guide](CONTRIBUTING.md) — how to work here
- [Content license policy](LICENSE-CONTENT.md) — code is MIT, Cortex-owned content is CC BY-SA

Its canonical repository is [github.com/refactor-ia/cortex](https://github.com/refactor-ia/cortex), maintained by RefactorIA.

---

<a href="https://github.com/Gentleman-Programming/gentle-ai">
  <img width="220" src="https://raw.githubusercontent.com/Gentleman-Programming/gentle-ai/main/docs/assets/brand/built-with-gentle-ai.png" alt="Built with Gentle-AI" />
</a>
