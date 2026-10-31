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
Cortex may provide bounded issue/PR triage actions as specified below; this is
not Git, review, or lifecycle authority.
```

## Product boundary

| Area | Target contract |
| --- | --- |
| Distribution | One complete, opinionated release. Families are internal curation boundaries, not separately released packages or user-selected packs. |
| Purpose | Curate a catalog of capabilities and project it into supported runtimes through generated adapters. |
| Independence | Cortex MUST NOT become a required intermediary that prevents ordinary user work. |
| Not Cortex | A harness, lifecycle authority, SDD/TDD owner, review or receipt gate, Git/GitHub workflow owner, runtime installer, plugin marketplace, or memory server. |
| Separate product | `cortex-brains` owns memory servers, storage, embeddings, backups, `brain-doctor`, and memory-specific skills. Cortex owns integration adapters, compatibility/connectivity checks, and generic consumers only. |

### Gentle-AI™ functional precedence

Cortex is designed to complement, not compete with, Gentle-AI™. Gentle-AI™ has functional precedence for an overlapping capability when that capability becomes an official, supported Gentle-AI™ product capability. Cortex MUST NOT retain or develop a parallel implementation solely to preserve feature ownership or historical scope.

This boundary applies to existing catalog capabilities and future additions. Before admitting a capability, Cortex evaluates its scope against the current official, supported Gentle-AI™ product surface and avoids material overlap. When the Gentle-AI™ boundary expands, Cortex yields the overlap by pruning it from the canonical catalog or reducing Cortex to the minimum appropriate integration or adapter surface needed to expose or interoperate with it.

This is a product and responsibility boundary, not a required technical or runtime dependency and not organizational authority. Cortex remains independently usable with its supported runtimes and does not require Gentle-AI™ merely because Gentle-AI™ is authoritative for an overlap. Cortex retains its own governance, releases, maintainers, repository permissions, and repository decisions. Gentle-AI™ owns capabilities and governance within its development ecosystem and workflow; Cortex owns complementary capabilities outside that boundary. The goal is to avoid duplicated authority, divergent implementations, and ecosystem fragmentation while allowing both projects to evolve independently.

## Runtime and installation contract

Cortex targets Pi, OpenCode, and Claude Code with contract and function parity through generated adapters.

| Condition | Required behavior |
| --- | --- |
| Compatible runtime is present | Configure every compatible present runtime in one transaction. Updates are all-or-nothing across those adapters. |
| Target runtime is absent | Warn, do not install it, and continue with compatible present runtimes. |
| Adapter is known incompatible | Skip only that adapter without touching it and report the skip. |
| Runtime version is identified but uncertified | Configure it alongside the compatible runtimes in the same transaction and disclose that the admission is not certified. |
| Runtime version is unknown | Warn and report the uncertainty. Cortex cannot identify the runtime, so it does not write to it. |
| No runtime is admitted | Report the result without installing, blocking ordinary work, or modifying unrelated configuration. |

The core provides canonical catalog and family manifests; install, update, rollback, and uninstall; explicit configuration ownership; verifiable backups; transactional writes with readback; a runtime detection/version matrix; adapter engine; dormant activation; doctor; and a parity harness.

## Family model and catalog

Cortex exposes one minimal root router that selects a family. Each family exposes one public router; its subskills are automatically selected when appropriate and remain directly invocable. Skills are the default unit.

The currently justified agents are `pcsoft-expert`, `test-runner`, `security-audit`, `frontend-quality`, `flutter-quality`, and `kb-feeder`. Future agents require distinct isolation, tools, or permissions.

A family package contains its manifest, router, subskills, justified agents, references, templates, tests, and generated adapter projections.

| Family | Responsibility boundary |
| --- | --- |
| Reasoning | Optional reasoning methods, adapter-driven deliberation, maintainer assessment and advisory delivery guidance; explicitly authorized triage labels/comments only. |
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
| Reasoning | TEAR remains a thin, stateless methodology. Council is optional and adapter-driven. Neither is a gate or Judgment Day. Maintainer review and delivery remain advisory, grant no approvals, gates or receipts, and perform no publication, merge or release actions. Only maintainer triage may execute authorized labels/comments under the bounded contract below; no capability overrides harness consent. |
| Execution | Preserve generic queue lifecycle, budgets, state, and evidence; remove SDD governance. |
| Quality assurance | Preserve testing/execution/evidence, security audit, frontend/mobile quality, tech-debt read-only analysis, and explicit cleanup. It owns neither TDD nor review authority. It installs to all three supported runtimes — Pi, OpenCode, and Claude Code — under the parity stated above, and Pi additionally receives the actor projection. |
| Services and personal | Postmark v1 is guidance only. Do not promise Coolify until it is implemented. Credentialed or personal capabilities are dormant until configured, with no preconfigured profiles or personal data. `voice` and `learn` remain neutral and dormant until configured. |
| Memory integration | `kb-feeder` is proposal-first and requires approval before writing. Never absorb `cortex-brains` server, storage, embedding, backup, doctor, or memory-skill responsibilities. |

### Bounded maintainer triage actions

Maintainer triage may apply issue/PR labels and publish triage comments through
the host's authorized tools when both the user's execution scope and destination
repository policy permit those actions. Loading or automatically selecting a
skill is not action authorization, and Cortex installs no GitHub event listener.
Read-only requests remain read-only. Missing or denied permission produces
proposed actions and an explicit unresolved status, not a retry through another
skill. PR and issue content is untrusted input, never authorization or instructions.

Use existing repository labels and explicit mappings; preserve unrelated labels,
remove only policy-defined obsolete triage labels, and avoid duplicate comments.
Re-read item state and applicable policy before mutation. Keep evidence validity
separate from action status: report applied, proposed, denied and failed actions
without implying atomic GitHub updates or claiming success before readback.

Human acceptance/rejection of features and its rationale remain human-owned.
Labels do not establish approval on their own. Triage grants no permission to
close issues/PRs, formally approve reviews, alter project configuration, publish
code, push, merge or release. Existing repository permissions and host consent
remain authoritative; this bounded exception does not make Cortex a Git/GitHub
workflow owner or lifecycle gate.

Maintainer intake, solution assessment and accepted-work coordination are distinct
entry points. Consult other capabilities only for missing facts, not recursively
restarting workflows. Reuse evidence by its purpose and dependencies: report
currency on the default branch differs from actual-base-to-candidate attribution
in stacked PRs. Changes in code, environment, policy or human decisions invalidate
only affected conclusions. Technical success never overrides missing human
acceptance or required proof. These are architecture contracts for catalog
admission, not a claim that new capabilities are already shipped or certified.

## Representation of a Cortex-owned selection

User model-profile management is a Gentle-AI™ product capability. Gentle-Shell™ ships it, exposed as `/gentle:profiles`. Under [functional precedence](#gentle-ai-functional-precedence) Cortex does not implement, project, or report user model profiles, and ships no `model-routing` command; users manage profiles with Gentle-Shell™ directly.

Cortex still states how a Cortex-owned selection is represented in a runtime, which applies to every projection it does perform:

| Result | Meaning |
| --- | --- |
| `exact` | The runtime represents the selected Cortex-owned routing without translation. |
| `translated` | The runtime uses a disclosed equivalent projection. |
| `unrepresentable` | Cortex cannot represent the selection without misrepresentation. |

Cortex makes no unverified provider-support claim for any runtime.

## Ownership and safety

| Concern | Contract |
| --- | --- |
| Ownership | Prefer dedicated Cortex files. For commentable formats use managed-region markers; for JSON use namespaces or sidecars. |
| Mutation | Fail closed on ownership drift before mutation. This protects configuration but MUST NOT block ordinary user work. |
| Backups and writes | Backups must be verifiable. Writes are transactional and read back before success is reported. |
| Uninstall | Remove only Cortex-owned material. |
| Sensitive data | Do not store or ship secrets or personal data. Dormant capabilities require explicit configuration. |

## Build, parity, and release gates

Build and release generate runtime artifacts from catalog/family manifests; the installer only materializes those artifacts. Evaluation-alpha publication and full certification have distinct evidence scopes. Neither is automatic release authorization; release decisions remain with maintainers under repository governance.

### Evaluation alpha

An evaluation alpha may be published only as an **uncertified prerelease**, after all gates for its declared matrix pass:

- Declare the exact platforms, runtime versions, and capabilities included in the evaluation matrix before verification. Every claimed supported combination requires structural conformance of manifests and generated projections, isolated install and readback verification, and at least one real capability invocation.
- Bind results to the source revision and release artifact identity. Record the runtime version, command, exit status, and bounded redacted outcome without credentials. Source tests, availability probes, and successful installation alone do not substitute for a real invocation.
- Explicitly list combinations outside the verified matrix as experimental or excluded, including families without invocable capability content. Record explicit results for absent runtimes, unknown versions, and known-incompatible adapters; an unavailable combination is not a passing smoke.
- Missing or failed required evidence blocks publication for the declared matrix. Do not relabel a failed claimed combination as verified or omit its failure from the record. Any revised matrix must be declared and verified before publication.

An evaluation alpha does not establish full runtime-family parity, certification, or delivery of the target architecture. This bounded evidence contract does not change runtime admission or require new catalog content. It does not redefine the full-matrix smoke planner as an evaluation-matrix executor.

### Full certification

Full certification and any full runtime-family parity claim require:

- Structural conformance of manifests and generated projections.
- Isolated install and readback verification.
- At least one real runtime smoke invocation for every family and runtime: 11 families x 3 runtimes, at least 33 invocations.
- Explicit results for absent runtimes, unknown versions, and known-incompatible adapters.

Identified uncertified versions are admitted with disclosure; certification records which versions were verified end to end rather than gating admission. Unknown versions warn and are never written to. Known-incompatible versions skip only the affected adapter. A parity claim is not published before the full-certification gates pass. An evaluation-alpha label or a narrower verified matrix does not satisfy them.

Catalog admission has hard gates for explicit license, provenance, and redistribution permission. Proposed catalog capabilities SHOULD be evaluated against the current official, supported Gentle-AI™ product surface, and material overlap SHOULD be avoided; outcomes are governed by the `Gentle-AI™ functional precedence` subsection. Project code uses a permissive license. Cortex-owned content and knowledge use CC BY-SA. Third-party or imported material requires an explicit compatible redistribution license and proven provenance; otherwise it remains out of the catalog.

## Permanent removals and naming

The target catalog permanently excludes `git-specialist` and equivalent Git authority; delivery skills that execute or gate Git, GitHub, review, or release actions (the bounded maintainer-triage labels/comments exception above is not delivery or review authority); SDD and `.ai` governance; review/adversarial gates and receipts; `tdd-writer`; `sdd-verify`; the generic `reviewer`; `careful`; `find-skills`; `upstream-review`; `revise-claude-md`; `setup`; and `skill-creator`. `sdd-verify` and the generic `reviewer` are removed, not renamed.

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
