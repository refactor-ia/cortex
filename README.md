# Cortex

Cortex is intended to become one curated open-source distribution of agent capabilities, maintained by RefactorIA and projected through generated adapters into Pi, OpenCode, and Claude Code while preserving user-owned configuration. Its canonical repository is [github.com/refactor-ia/cortex](https://github.com/refactor-ia/cortex).

> **Current status — foundations, not a release.** This repository contains the target architecture and community foundation. **Implementation is not complete:** Cortex is not yet an end-to-end product, does not claim runtime parity, and has no certified release path.

## What works today—and what does not

| Status | Scope |
| --- | --- |
| **Executable today** | Read-only `doctor`; conservative `uninstall` of exact Cortex-owned state and artifacts. |
| **Implemented foundations** | Catalog schemas, loading, admission, and snapshots; rendering, projection, and artifact planning; the runtime matrix; and transactional install/update primitives. These are not yet an end-to-end product. |
| **Target only** | The capability catalog, family packages, agent prompts, model-profile definitions, runtime parity, and release. |

Only the exact runtime versions in the evidence table below are admitted; unknown versions remain fail-closed and report-only. There are intentionally no installation instructions or quick-start path in this README.

## The product direction

Cortex curates one complete distribution rather than separate releases or user-selected packs. It will project approved capabilities into compatible runtimes through generated adapters; it does **not** install Pi, OpenCode, or Claude Code. When the target lifecycle is certified, Cortex will detect compatible runtimes already present, configure each compatible present runtime transactionally, and warn about absent, unknown, or incompatible runtimes without taking over ordinary work.

Cortex complements rather than competes with Gentle AI™; the [functional-precedence boundary](docs/architecture/overview.md#gentle-ai-functional-precedence) is normative.

For the full intended contract, see the [architecture overview](docs/architecture/overview.md). The [documentation map](docs/README.md) points to the repository authorities.

## Target capability families

Families are internal curation boundaries, not separately released packages or user-selected packs.

| Family | Responsibility boundary |
| --- | --- |
| Reasoning | Optional, stateless reasoning methods and adapter-driven deliberation. TEAR remains thin and stateless; Council is optional and adapter-driven. Neither is a gate or Judgment Day. |
| Model intelligence | User-directed model-role configuration and representation reporting. |
| Execution | Generic queue lifecycle, budgets, state, and evidence—without SDD governance. |
| Quality assurance | Testing, execution evidence, security audits, frontend/mobile quality, read-only technical-debt analysis, and explicit cleanup—without TDD or review authority. |
| Web | Curated web-development capabilities, subject to provenance review. |
| Mobile | Curated mobile-development capabilities, subject to provenance review. |
| PCSoft | Help understanding and working from WLanguage/HFSQL source; automatic migration and support services are out of scope. |
| Services | Service guidance activated only after required configuration. Postmark v1 is guidance only; Coolify is not promised until implemented. |
| Personal | Personal capabilities remain dormant until explicitly configured. `voice` and `learn` are target capabilities with no preconfigured profiles or personal data. |
| Memory integration | Generic consumers and `cortex-brains` integration adapters only. `kb-feeder` is proposal-first and requires approval before writing. |
| Documentation | Documentation guidance, guides, RFCs, onboarding, and review-facing docs. |

## Target agents, not shipped agents

The architecture currently justifies these agent IDs. They are target catalog members, not a claim that prompts or runtime projections are shipped.

| Agent ID | Grounded purpose |
| --- | --- |
| `pcsoft-expert` | Help understand and work from WLanguage/HFSQL source. |
| `test-runner` | Support testing and execution evidence within quality assurance. |
| `security-audit` | Perform security audit work within quality assurance. |
| `frontend-quality` | Analyze frontend quality within quality assurance. |
| `flutter-quality` | Analyze mobile/Flutter quality within quality assurance. |
| `kb-feeder` | Propose memory-integration contributions; approval is required before writing. |

## Model-routing target

Cortex targets four user-directed routing profiles: `openai`, `nan`, `mixed`, and `anthropic`. A future installation must not choose a profile automatically, replace existing user routing, or apply routing outside Cortex-owned roles.

Each runtime projection will report whether the selected Cortex-owned routing is **exact** (represented without translation), **translated** (a disclosed equivalent projection), or **unrepresentable** (cannot be represented honestly). An unrepresentable runtime remains untouched. Model-routing status and reasoning status are separate reports; provider support for Pi and OpenCode is not claimed until verified.

## Ownership and safety

- Cortex prefers dedicated files; where necessary, it uses managed regions, namespaces, or sidecars to identify its material.
- It fails closed on ownership drift before mutation, protecting user configuration without blocking ordinary work.
- Target writes are transactional, read back before success is reported, and roll back on failure; backups must be verifiable.
- Uninstall removes only Cortex-owned material.
- Cortex does not store or ship secrets or personal data. Credentialed and personal capabilities remain dormant until explicitly configured.

## Runtime admission evidence

The following isolated marker installation, readback, acknowledgement, and cleanup evidence succeeded with zero retries, exit 0, and no timeouts or overflows. Marker `98372fc8807c2965cb1062664614ea8c1773f240bca7fb97c2d1dcb78b9fe3f6`; snapshot `6f08ee25dc84c7cba2be78deab7eeaca8585d5fa1528795a9256e642854fac88`.

| Runtime | Exact version | Evidence records | Duration |
| --- | --- | --- | --- |
| Pi | 0.85.1 | source `b3364d923bf20abc242c7bd4cae25ec07731bc36`; command `71a9ab3f17ac06a26c31e61eafdeb7131ab2dfdf2ce9735dd9cc6a0b97349e75` | 9345 ms |
| OpenCode | 1.18.25 | source `07f8979cf4969f0bb977d401e3792bd5d754e963`; command `6062b37fc943e60d1828a27ed69ed738b8572b1a27bacd068f8a679de19f2f8a`; config `61e3b70f80acc12049f71307760e500778f288b30dc0ee9d42c3cb43b091f3e0`; `skill_tool_completed=true` | 5000 ms |
| Claude Code | 2.1.251 | source `f68758e784287cff78599c24db8632a13890c206`; command `0cba56fe517827ac8169a49cd36291d13875d06883be4729654fa34183aa8133`; schema `f7374878f385a402aa47362c62c882bdafba7abafac4d266ab81047448a5d46b`; auth `subscription_oauth_token` | 2342 ms |

This is exact-version admission only. It is not full 11×3 runtime-family parity and is not release certification.

## Release evidence and catalog admission

A future parity claim requires structural conformance, isolated installation and readback, and at least one real smoke invocation for every family and runtime: 11 families across 3 runtimes, for at least 33 invocations. Release evidence must also cover absent runtimes, unknown versions, and known-incompatible adapters.

Catalog content is admitted only with explicit licensing, provenance, and redistribution permission. Cortex-owned knowledge and capability content uses CC BY-SA; third-party material stays outside the catalog until compatible redistribution rights are proven.

## What Cortex is not

Cortex has no Git or GitHub authority; no SDD, TDD, or review authority; no lifecycle-governance harness; and no memory-server ownership. `cortex-brains` remains a separate product for memory servers, storage, embeddings, backups, and memory-specific operations.

## Near-term direction

The repository direction is to complete a certified install/update lifecycle and three-runtime parity harness, then populate and migrate the catalog and pass release gates. No delivery date is promised.

## Contribute and learn more

- [Architecture overview](docs/architecture/overview.md)
- [Documentation map](docs/README.md)
- [Contributing guide](CONTRIBUTING.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Governance](GOVERNANCE.md)
- [Security Policy](SECURITY.md)
- [Support](SUPPORT.md)
- [MIT License](LICENSE) and [content license policy](LICENSE-CONTENT.md)
