# Design: Fork First, Upstream Last (#3)

Status: proposed specification. This document defines the canonical policy, its durable
source, projection contract, and lifecycle behavior. Implementation (Go/tests) is tracked
in child issues and is out of scope here.

## 1. Policy statement

**Fork First, Upstream Last** is Cortex's canonical user-global construction policy:

1. Private repositories remain outside this policy unless another explicit rule includes
   them. Unknown visibility is treated as public until confirmed otherwise.
2. Every public repository is constructed through the authenticated user's fork —
   including one-line changes and repositories where the user is an owner or maintainer.
3. Canonical upstream, fork ownership, visibility, and default branch are verified
   independently of remote names (`origin`, `upstream`, `fork`, and directory basenames
   are never evidence).
4. Official upstream is used for review, CI, branch protection, maintainer approval, and
   final delivery — never as the construction workspace.
5. Every commit, push, fork creation, issue, pull request, or upstream mutation still
   requires its normal explicit authorization. This policy changes where work happens;
   it grants no permission.
6. For dependent work, the complete suite is assembled and tested on a fork-owned
   integration branch. Upstream receives clean, coherent submission branches only when
   the work is mature — never an integration-branch mega-PR and never blocked chains.
7. Fail closed: if repository visibility or canonical ownership cannot be verified, the
   workflow stops and asks, rather than guessing.

## 2. Canonical source and precedence

- The canonical, complete policy text is the versioned policy asset shipped by Cortex
  (asset kind `policy-doc`, id `fork-first-upstream-last`, format version 1). The asset
  is embedded in the built-in catalog exactly like skills and QA actors, and is the sole
  source of truth; any rendered projection is derived from it.
- Exact readback rule: projections carry a marker-embedded header bound to the asset
  fingerprint. If a projected file is edited externally, ownership observation reports
  the drift and the file is treated as user-owned guidance (Preserve/Conflict semantics,
  never silently overwritten) — the same contract used for QA actors.
- Precedence, in order:
  1. system/platform safety rules and the current explicit instruction;
  2. this global policy;
  3. stricter repository-local rules (including each repository's own issue-first,
     approval, review-size, CI, and branch-protection requirements).
  Operation-specific authorization applies only to the named operation and never lowers
  this precedence.
- User-owned configuration and project-local rules are preserved; this policy never
  claims ownership over them.

## 3. Routing contract (implementation requirements)

- Identity verification resolves the GitHub `owner/repo` from the push target's
  authenticated remote URL and cross-checks visibility through the API; remote names and
  directory names are never trusted.
- On a public repository without a verified fork: the workflow stops and requests
  authorization before creating the fork. Existing remotes are never renamed, and dirty
  or unrelated working trees are never touched.
- Maintainer or owner write access to upstream is not an exemption (upstream write
  permission must not become a construction exception).
- Promotion to upstream: refresh from the official default branch, then produce coherent
  per-change submission branches.

## 4. Lifecycle

- `install`/`update` project the policy into each supported runtime's user-global
  instruction surface, reusing the single pipeline
  (`skillrender` → `adapterplan` → `skillprojection` → `skillartifact` → destination →
  plan → transaction), with a new `policy-doc` asset kind paralleling `pi-actor`.
- `doctor` reports the observed state per runtime: present-and-current, drifted
  (user-edited; preserved, with an explicit note), or absent.
- `uninstall` removes projections it owns and leaves user-owned or externally modified
  files untouched (blocking, with an explicit report, when shared actors prevent safe
  removal).
- Runtime projection matrix: Pi, OpenCode, and Claude Code each receive the same
  effective policy; where a runtime cannot represent it exactly, the projection is
  disclosed as a translation, following the existing disclosure contract.

## 5. Non-goals

- No automatic fork creation, remote renaming, commit, push, issue, or pull request.
- No weakening of repository-specific approval, size, review, CI, or protection rules.
- Not published as a default for consumers of unrelated packages.
- No repository-local adoption elsewhere without that repository's own approved issue.

## 6. Verification plan (implementation issues)

Tests must cover: fresh-session loading, public routing to the fork, non-semantic remote
names, maintainer access without exemption, unknown visibility failing closed, private
exclusion, missing-fork authorization stop, no remote renaming, fork-owned integration
branches, upstream promotion gates, dirty-tree preservation, and user-config
preservation — plus `tests/test_community_policy.py` inventory updates for any new
published file.
