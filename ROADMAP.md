# Cortex Roadmap

Where Cortex is going after the foundation phase. This document states direction and
priorities; the [target architecture](docs/architecture/overview.md) remains the
authority for contracts and boundaries. Anything here that contradicts the architecture
is an error in this document, not permission to diverge.

## North star

Cortex is a curated, open-source distribution of agent capabilities for Pi, OpenCode,
and Claude Code, installed with package-manager-grade guarantees: transactional writes,
verifiable backups, explicit file ownership, readback, and fail-closed behavior on
uncertainty.

The foundation phase built those guarantees and proved them. The next phase is
**content on top of that base**: complete verticals, one family at a time, with
collaboration as a first-class delivery path. New horizontal infrastructure requires a
demonstrated need (see issue #175).

Sequencing and readiness are coordinated through the living ODD roadmap index
(issue #288, described in the [operating guide](docs/operating.md)); this document
holds the narrative, the index holds the approved order.

## Current state (honest)

| Area | State |
| --- | --- |
| Lifecycle core | Shipped and tested: `doctor`, transactional `install`/`update`, conservative `uninstall`, backups, rollback, ownership observation. |
| QA family | Six role capabilities plus `qa-no-ci`, multi-runtime QA backend (`qa run`) and evidence sidecars; complete published-binary live-model showcase still unproven. |
| Reasoning and documentation | Maintainer triage, review and delivery; guide-writing and ODD roadmap. Twelve capabilities across three populated families; eight families remain without capability content. |
| Releases | Alpha20 published for evaluation, with verified artifacts and real macOS/Linux deployments; no full runtime-family or release certification claim. |
| Collaboration | CONTRIBUTING, governance, code of conduct, and security policy in place; first `good first issue`s published. |

## Beta1 exit criteria

The next bounded milestone is **v0.1.0-beta.1**. Freeze the current capability scope while establishing its evidence; more families or features are not a prerequisite. There is no promised delivery date. These criteria define the desired outcome, not automatic release authorization or a new ordering of the issue #288 work index.

| Gate | Required evidence before beta | Current gap |
| --- | --- | --- |
| Declare the supported matrix | Name exact platform/runtime versions and current capabilities included in beta. For each claimed supported combination, record structural checks, isolated install/readback and a real capability invocation with attributable results. Explicitly mark other combinations experimental or excluded. | Installation and readback exist, but complete real-invocation coverage for a declared beta matrix does not. |
| Prove the QA journey | Fresh installation from the published binary, a user-configured authenticated model route, a real QA request and a valid bounded report. Record version, command, exit and redacted output without credentials. | End-to-end live-model showcase not yet proven by the current release evidence. |
| Resolve or exclude known blockers | Reproduce the Pi guidance update refusal and local QA/update baseline failures; fix and verify them, or state a precise unsupported case with a safe operator path. A green CI run does not erase contrary local evidence. | Guidance adoption and baseline failures require explicit disposition; no blanket compatibility claim. |
| Make lifecycle and recovery actionable | Verify documented download/checksum, install, prior-version update, refusal, backup, partial-failure recovery and uninstall paths for the declared matrix. Failed checks must prevent writes/extraction where applicable. | Artifact and native lifecycle evidence exists; complete matrix/recovery coverage still needs verification. |

A private machine being offline is not a beta gate. The supported beta matrix may be narrower than the target architecture, but exclusions must be explicit, not described as parity. Full eleven-family by three-runtime certification retains the architecture's separate evidence requirement; a beta label does not satisfy it.

An evaluation alpha may use the architecture's [bounded prerelease contract](docs/architecture/overview.md#evaluation-alpha): declare exact platforms, runtime versions and capabilities, pass structural checks, isolated install/readback and a real invocation for every claimed supported combination, and disclose exclusions and unavailable or incompatible cases. It remains uncertified; it does not complete the beta criteria or the full-certification matrix merely by being published. Record its observed scope in [verification evidence](docs/verification.md#evaluation-alpha-evidence-scope).

Current evidence and limitations: [alpha20 delivered evidence](docs/verification.md#alpha20-delivered-evidence).

## Tracks

### A. Complete the QA flagship

QA is the demo vertical: it shows what "curated capability with guarantees" means in
practice.

- Finish the QA agent fleet epic (#78): explicit opt-in external bridge from Claude
  Code to one selected Pi/OpenCode backend, user-owned capability profiles, bounded
  attributable evidence.
- One end-to-end showcase: analyze a real artifact (a PR, a document) with the QA
  fleet and return bounded evidence, from a fresh install.
- Work toward the architecture's release gates for the QA family on all three runtimes
  (structural conformance, isolated install and readback, real smoke invocations).

### B. Content verticals, in order

Each vertical starts only when the previous one is seeded or complete; sequence keeps
focus and gives collaborators a stable surface.

1. **Documentation family** (already seeded): extend guide-writing and ODD roadmap
   with scoped guides, RFCs, onboarding and review-facing documentation capabilities
   after the beta evidence work. Content still requires provenance admission.
2. **Web family** (first curated technical vertical): web-development capabilities
   under the architecture's provenance review.
3. **PCSoft family**: WLanguage/HFSQL capabilities; conditioned on the
   `cortex-brains` companion product shipping first, per the target architecture.
4. **Reasoning and execution families**: complete the already-defined thin
   methodologies (TEAR) and generic queue lifecycle with concrete capability content.

### C. Collaborator experience

The project is now open; onboarding must be excellent or the invitation is empty.

- Verify the README quick-start against every release archive (#279).
- CLI-level test coverage for diagnostic output (#280).
- Honest diagnostics for the install lifecycle: no opaque `transaction_failed` after
  `doctor` exit 0 (#281).
- Keep CONTRIBUTING aligned with the actual first-PR path; maintain a set of
  `good first issue`s that are real, scoped, and reviewable.

### D. Robustness and governance (background)

These run only when they unblock A/B:

- Seam compatibility from measured negotiated contracts, not parsed version strings
  (#235).
- Detect Gentle-AI™ capability overlap before it becomes a yield (#207).
- Implement the project constitution (#146) and the fork-first policy (#3) as their
  child issues define.

## Sequencing

1. Now: freeze current capabilities and close the beta1 evidence gates, using Track A
   for the QA journey and Track C for verified operator instructions.
2. Resolve or explicitly exclude compatibility and recovery blockers within the
   declared beta matrix; do not expand content to compensate for missing evidence.
3. After that bounded milestone, resume Track B expansion through the approved work index.
4. Track D remains service work only when needed to unblock those outcomes.

## Collaboration

Start with the [quick-start](README.md#start-in-two-minutes), read
[CONTRIBUTING](CONTRIBUTING.md), and pick a `good first issue`. Capability content
(family work in Track B) is the most valuable and most accessible contribution surface:
it requires judgment and writing, not transaction internals.
