# Design: Project Constitution (#146)

Status: proposed specification. This document defines the constitution artifact, its
format, ownership, projection contract, and lifecycle behavior. Implementation
(Go/tests) is tracked in child issues and is out of scope here.

## 1. Purpose and boundaries

The constitution records a project's **mandatory foundations** so that any agent or
contributor can detect conflicts before implementation planning. It is not general OSS
governance (owned by GOVERNANCE.md/CONTRIBUTING.md), not an architecture document
(owned by `docs/architecture/overview.md`), and not a specification format. It holds
only durable, mandatory choices and their rationale.

## 2. Artifact and location

- Canonical artifact: a single versioned document per project,
  `constitution.md`, format version 1, stored as a Cortex-owned `policy-doc` asset kind
  (the same kind proposed for #3), so it is embedded, fingerprint-bound, and projected
  through the existing single pipeline.
- Per-project instances are created by running Cortex in the target project with an
  explicit, human-authorized initialization; Cortex never amends a constitution
  implicitly while implementing unrelated work.
- Initial Cortex constitution: **carries no stack mandates.** The historical Svelte
  (frontend) and PostgreSQL (persistence) mandates were reviewed and explicitly
  retired as obsolete on 2026-10-08; they are not re-imported.

## 3. Format (version 1)

Markdown with a metadata header and five fixed sections; each mandate is one entry
with owner, status, and rationale:

```markdown
---
constitution: v1
project: <canonical project identity>
amended: <ISO-8601 date>
---
## Stack
## Conventions
## Security Restrictions
## Testing Standards
## Technical Decisions
```

- Mandatory rules are marked `mandatory:`; everything else is a recommendation and
  must not be treated as policy.
- Each entry records its rationale so future conflicts can be evaluated against intent,
  not just text.
- Duplicates of rules owned elsewhere are forbidden (link instead).

## 4. Semantics: read / not_available / conflict

Pre-implementation inspection reports exactly one of:

- `read` — constitution present, parseable, supported version; rules loaded.
- `not_available` — absent, malformed, or unsupported version. Fail closed: mandatory
  foundations cannot be assumed; planning must surface the gap instead of guessing.
- `conflict` — the proposed work contradicts a `mandatory` entry. Planning must stop
  and surface the specific entries; conflicts are resolved by explicit amendment or by
  changing the work, never by silent divergence.

Precedence: platform safety and current explicit instruction → constitution
(mandatory entries) → other project-local instructions. Recommendations never override
anything.

## 5. Runtime projection

Pi, OpenCode, and Claude Code receive the equivalent effective constitution from the
single canonical document; where a runtime cannot represent it exactly, the projection
is disclosed as a translation, following the existing disclosure contract. Externally
edited projections are observed as user-owned drift (Preserve/Conflict, never silently
overwritten), reusing the marker-bound exact-readback contract.

## 6. Lifecycle

- `install`/`update` project the runtime guidance for reading/preceding over the
  constitution; the canonical `constitution.md` itself is project-owned and is only
  created or changed through explicit, reviewable amendments.
- `doctor` reports, per runtime: guidance present-and-current, drifted (preserved with
  a note), or absent; and for project instances: present, malformed, unsupported
  version, or absent.
- `uninstall` removes only Cortex-owned projections; a project's `constitution.md` is
  user-owned content and is never removed by uninstall.
- Amendments require their own explicit, reviewable change (issue-first in projects
  using this workflow); agents must not mutate the constitution as a side effect.

## 7. Verification plan (implementation issues)

Tests must cover: present, absent, malformed, unsupported-version, mandatory-vs-
recommendation precedence, conflict surfacing, amendment authorization, projection
parity across the three runtimes, external-edit preservation, uninstall behavior, and
`tests/test_community_policy.py` inventory updates for any new published file.
