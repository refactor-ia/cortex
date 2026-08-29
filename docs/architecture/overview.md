# Cortex target architecture

## Decision and status

Cortex will become a curated open-source distribution of agent capabilities maintained by RefactorIA. Its canonical future repository is [github.com/refactor-ia/cortex](https://github.com/refactor-ia/cortex).

This document is the authoritative target architecture. It defines the intended product contract, not the current implementation or a release guarantee. This clean repository contains the target architecture and community foundation. Legacy source material remains outside this repository and does not define it.

## System context

```text
                         User-owned configuration
                                  |
                                  v
+-------------+     generated adapters     +-----------------------------+
| Cortex      | ------------------------> | Pi | OpenCode | Claude Code   |
| catalog and | <--- detection/results --- | present compatible runtimes  |
| adapter core|                            +-----------------------------+
+------+------+                                        |
       |                                               |
       | integration adapters                          | optional connections
       v                                               v
+--------------------+                         +------------------+
| cortex-brains      |                         | User services,   |
| separate product:  |                         | credentials, and |
| memory operations  |                         | personal data    |
+--------------------+                         +------------------+

Optional harnesses may govern lifecycle, Git, TDD, SDD, or review. Cortex does not.
```

## Product boundary

| Area | Target contract |
| --- | --- |
| Distribution | One complete, opinionated release. Families are internal curation boundaries, not separately released packages or user-selected packs. |
| Purpose | Curate a catalog of capabilities and project it into supported runtimes through generated adapters. |
| Independence | Cortex MUST NOT become a required intermediary that prevents ordinary user work. |
| Not Cortex | A harness, lifecycle authority, SDD/TDD owner, review or receipt gate, Git/GitHub workflow owner, runtime installer, plugin marketplace, or memory server. |
| Separate product | `cortex-brains` owns memory servers, storage, embeddings, backups, `brain-doctor`, and memory-specific skills. Cortex owns integration adapters, compatibility/connectivity checks, and generic consumers only. |

### Gentle-AI functional precedence

Cortex is designed to complement, not compete with, Gentle-AI. Gentle-AI has functional precedence for an overlapping capability when that capability becomes an official, supported Gentle-AI product capability. Cortex MUST NOT retain or develop a parallel implementation solely to preserve feature ownership or historical scope.

This boundary applies to existing catalog capabilities and future additions. Before admitting a capability, Cortex evaluates its scope against the current official, supported Gentle-AI product surface and avoids material overlap. When the Gentle-AI boundary expands, Cortex yields the overlap by pruning it from the canonical catalog or reducing Cortex to the minimum appropriate integration or adapter surface needed to expose or interoperate with it.

This is a product and responsibility boundary, not a required technical or runtime dependency and not organizational authority. Cortex remains independently usable with its supported runtimes and does not require Gentle-AI merely because Gentle-AI is authoritative for an overlap. Cortex retains its own governance, releases, maintainers, repository permissions, and repository decisions. Gentle-AI owns capabilities and governance within its development ecosystem and workflow; Cortex owns complementary capabilities outside that boundary. The goal is to avoid duplicated authority, divergent implementations, and ecosystem fragmentation while allowing both projects to evolve independently.

## Runtime and installation contract

Cortex targets Pi, OpenCode, and Claude Code with contract and function parity through generated adapters.

| Condition | Required behavior |
| --- | --- |
| Compatible runtime is present | Configure every compatible present runtime in one transaction. Updates are all-or-nothing across those adapters. |
| Target runtime is absent | Warn, do not install it, and continue with compatible present runtimes. |
| Adapter is known incompatible | Skip only that adapter without touching it and report the skip. |
| Runtime version is unknown | Warn and report the uncertainty. |
| No compatible runtime is present | Report the result without installing, blocking ordinary work, or modifying unrelated configuration. |

The core provides canonical catalog and family manifests; install, update, rollback, and uninstall; explicit configuration ownership; verifiable backups; transactional writes with readback; a runtime detection/version matrix; adapter engine; dormant activation; doctor; and a parity harness.

## Family model and catalog

Cortex exposes one minimal root router that selects a family. Each family exposes one public router; its subskills are automatically selected when appropriate and remain directly invocable. Skills are the default unit.

The currently justified agents are `pcsoft-expert`, `test-runner`, `security-audit`, `frontend-quality`, `flutter-quality`, and `kb-feeder`. Future agents require distinct isolation, tools, or permissions.

A family package contains its manifest, router, subskills, justified agents, references, templates, tests, and generated adapter projections.

| Family | Responsibility boundary |
| --- | --- |
| Reasoning | Optional, stateless reasoning methods and adapter-driven deliberation. |
| Model intelligence | User-directed model-role configuration and representation reporting. |
| Execution | Generic queue lifecycle, budgets, state, and evidence without SDD governance. |
| Quality assurance | Testing, execution evidence, audits, quality analysis, and explicit cleanup without TDD or review authority. |
| Web | Curated web-development capabilities, subject to provenance review. |
| Mobile | Curated mobile-development capabilities, subject to provenance review. |
| PCSoft | Capabilities that help understand and work from WLanguage/HFSQL source. Automatic migration to a destination stack and support services are outside the product. |
| Services | Service guidance with activation only after required configuration. |
| Personal | Personal capabilities that remain dormant until explicitly configured. |
| Memory integration | Generic consumers and `cortex-brains` integration adapters only. |
| Documentation | Documentation guidance, guides, RFCs, onboarding, and review-facing docs. |

### Family-specific constraints

| Family | Target constraint |
| --- | --- |
| Reasoning | TEAR remains a thin, stateless methodology. Council is optional and adapter-driven. Neither is a gate or Judgment Day. |
| Execution | Preserve generic queue lifecycle, budgets, state, and evidence; remove SDD governance. |
| Quality assurance | Preserve testing/execution/evidence, security audit, frontend/mobile quality, tech-debt read-only analysis, and explicit cleanup. It owns neither TDD nor review authority. |
| Services and personal | Postmark v1 is guidance only. Do not promise Coolify until it is implemented. Credentialed or personal capabilities are dormant until configured, with no preconfigured profiles or personal data. `voice` and `learn` remain neutral and dormant until configured. |
| Memory integration | `kb-feeder` is proposal-first and requires approval before writing. Never absorb `cortex-brains` server, storage, embedding, backup, doctor, or memory-skill responsibilities. |

## Model routing

Cortex recognizes `openai`, `nan`, `mixed`, and `anthropic` profiles. Installation MUST NOT auto-select a profile, replace existing user routing, or apply routing outside Cortex-owned roles. Profile application is all-or-nothing across present runtimes that can represent it; an unrepresentable runtime remains untouched. Model status and reasoning status are reported separately.

Each runtime projection reports one result:

| Result | Meaning |
| --- | --- |
| `exact` | The runtime represents the selected Cortex-owned routing without translation. |
| `translated` | The runtime uses a disclosed equivalent projection. |
| `unrepresentable` | Cortex cannot represent the selection without misrepresentation. |

Claude Code reports `openai`, `nan`, and the current `mixed` profile as `unrepresentable`; `anthropic` may map the model exactly while reasoning support is reported separately. Cortex makes no unverified provider-support claim for Pi or OpenCode.

## Ownership and safety

| Concern | Contract |
| --- | --- |
| Ownership | Prefer dedicated Cortex files. For commentable formats use managed-region markers; for JSON use namespaces or sidecars. |
| Mutation | Fail closed on ownership drift before mutation. This protects configuration but MUST NOT block ordinary user work. |
| Backups and writes | Backups must be verifiable. Writes are transactional and read back before success is reported. |
| Uninstall | Remove only Cortex-owned material. |
| Sensitive data | Do not store or ship secrets or personal data. Dormant capabilities require explicit configuration. |

## Build, parity, and release gates

Build and release generate runtime artifacts from catalog/family manifests; the installer only materializes those artifacts. A release requires:

- Structural conformance of manifests and generated projections.
- Isolated install and readback verification.
- At least one real runtime smoke invocation for every family and runtime: 11 families x 3 runtimes, at least 33 invocations.
- Explicit results for absent runtimes, unknown versions, and known-incompatible adapters.

Unknown versions warn. Known-incompatible versions skip only the affected adapter. A parity claim is not published before these gates pass.

Catalog admission has hard gates for explicit license, provenance, and redistribution permission. Proposed catalog capabilities SHOULD be evaluated against the current official, supported Gentle-AI product surface, and material overlap SHOULD be avoided; outcomes are governed by the `Gentle-AI functional precedence` subsection. Project code uses a permissive license. Cortex-owned content and knowledge use CC BY-SA. Third-party or imported material requires an explicit compatible redistribution license and proven provenance; otherwise it remains out of the catalog.

## Permanent removals and naming

The target catalog permanently excludes `git-specialist` and equivalent Git authority; the delivery family; SDD and `.ai` governance; review/adversarial gates and receipts; `tdd-writer`; `sdd-verify`; the generic `reviewer`; `careful`; `find-skills`; `upstream-review`; `revise-claude-md`; `setup`; and `skill-creator`. `sdd-verify` and the generic `reviewer` are removed, not renamed.

Naming changes make the retained boundaries visible:

| Historical name | Target name |
| --- | --- |
| `verify` | `test-runner` |
| `security-reviewer` | `security-audit` |
| `frontend-reviewer` | `frontend-quality` |
| `flutter-reviewer` | `flutter-quality` |
| `capability-authoring` | `documentation` |

## Migration and release sequence

Migration is incremental, not a big-bang rewrite:

1. Reconcile root, main, and worktree authority.
2. Define schemas and core services.
3. Build adapters and the parity harness.
4. Remove no-rescue surfaces.
5. Extract execution and quality assurance before removing historical governance.
6. Migrate families.
7. Run full release gates.

`cortex-brains` remains a separate product and ships before the PCSoft-focused Cortex milestone. This clean repository imports approved public artifacts only and does not inherit legacy history wholesale.

## Open decisions

The following decisions remain open and must not be inferred from this document:

- Exact manifest schema.
- Public disclosure policy for NaN.
- Provenance audit for web and mobile material.
- Final release name and version.
- Repository-transfer timing.

## Review checklist

- [ ] The proposed change keeps Cortex independent from harness, Git, review, and lifecycle authority.
- [ ] It preserves one-distribution curation and the approved family boundaries.
- [ ] It respects explicit configuration ownership, dormant activation, and user routing.
- [ ] It does not claim target runtime parity, installation behavior, or release completion before verification.
- [ ] It does not assign `cortex-brains` responsibilities to Cortex.

## Related authority

Read the [documentation map](../README.md) for the authority map and the [root README](../../README.md) for public product identity and transition status.
