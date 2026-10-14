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
| QA family | The one vertical with real content: 6 role capabilities plus `qa-no-ci`, multi-runtime QA backend (`qa run`), evidence sidecars. |
| Other 10 families | Manifest and router skeletons only; no capability content yet. |
| Releases | Evaluation prereleases (alpha series); no certified release path yet. |
| Collaboration | CONTRIBUTING, governance, code of conduct, and security policy in place; first `good first issue`s published. |

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

1. **Documentation family** (first community vertical): guides, RFCs, onboarding, and
   review-facing documentation capabilities. No provenance risk, no credentials, ideal
   first surface for contributors.
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

1. Now: Track C (days) — the invitation must work before it is amplified.
2. In parallel: Track A toward a complete QA vertical.
3. Then: Track B item 1 (documentation) as the first community-built vertical.
4. Ongoing: Track D as service work, never as the primary goal.

## Collaboration

Start with the [quick-start](README.md#start-in-two-minutes), read
[CONTRIBUTING](CONTRIBUTING.md), and pick a `good first issue`. Capability content
(family work in Track B) is the most valuable and most accessible contribution surface:
it requires judgment and writing, not transaction internals.
