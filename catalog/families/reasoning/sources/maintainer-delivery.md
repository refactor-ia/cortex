# Maintainer Delivery

<!-- cortex-reasoning:advisory-only -->
<!-- cortex-reasoning:executes-and-gates-nothing -->
<!-- cortex-reasoning:never-overrides-harness-consent -->
<!-- cortex-reasoning:operator-decides-every-step -->

Goal: shortest safe time from taking an issue to merging it, with several PRs
in flight without losing the thread. Guidance only: this skill executes and
gates nothing. Every label, comment, commit, push, PR, review consent, and merge
is the operator's decision through their own tooling, and it never overrides
the harness workflow or its consent prompts.

## 0. Preflight (seconds)

- Local clone current with `origin/main`; one fresh worktree per issue.
- Harness ready: model profiles present, so native review can resolve its
  models. A missing profile file surfaces mid-review as "no model configured".

## 1. Take the issue (about 2 minutes)

- Race check: issue open and unassigned, no open or merged PR references it,
  note the `origin/main` SHA. Competing PR found: stop. See "Race gate" below.
- Follow the repo's issue-first policy (CONTRIBUTING): e.g. `status:approved`
  plus `type:*`, and a short claim comment with scope and out-of-scope.
- Read the conventions from the repo, not from memory: PR template
  (`.github/PULL_REQUEST_TEMPLATE.md` or similar), and branch and title
  patterns of the last merged PRs (e.g. `fix/<issue>-<slug>`,
  `fix(<scope>): <summary>`).

## 2. Kickoff prompt

Fill and send:

```
Fix <repo>#<N>. Expected behavior: <exact>. Test-first.
Out of scope: <list>. Branch: <convention>. Run the focused test and the
affected suite; stop before commit and show the diff.
```

Scope growth is the main cause of slow merges. Keep about 150 changed lines or
3 files; beyond that, stop and re-scope or split.

## 3. Checks

- Focused test red then green; affected suite run.
- Run locally what CI runs before pushing, so the first CI run is green.
- Red suite: compare against pristine `origin/main` before calling anything
  pre-existing. See "Baseline differential diagnosis" below. Never call red
  green.

## 4. Review and PR

- Native review as the harness offers it; consent is the operator's.
- Commit only the change (no task or scratch files), conventional message.
- Push the branch (no `--force`, never `main`), repeat the race check, open
  the PR with the repo template: `Closes #N`, verification (command, result,
  SHA), advisories, out-of-scope.

## 5. While waiting: next issue

CI, bots, and reviews are wait time, not work. Start the next issue in another
worktree. Keep one line per PR in flight so switching costs nothing:

```
<repo>#<PR>  <branch>  <step: review|ci|feedback|ready>  <next action>
```

## 6. Feedback

Collect all review comments (humans and bots) and answer in one batch: one
correction commit, one re-check, one reply. Same day when possible.

## 7. Merge

Self-merge is reasonable when all hold: CI green, native review approved, bot
reviews without blocking findings, within budget, and no sensitive area
(security, installers, review or release machinery, migrations, data).
Otherwise ask another maintainer to review. Right before merging: race check
again, issue still open, `main` not moved in a conflicting way.

## Measure

Time from taking the issue to merge, split into active work and waiting (CI,
bots, humans). Also PRs in flight. A slow merge caused by waiting is fixed by
step 5, not by skipping checks.

---

## Race gate (operator-run, advisory)

Example commands the operator can run at intake (before claiming an issue)
and again immediately before opening a PR. These are suggestions, not
actions this skill takes — the operator decides whether and when to run
them, and what to do with the results.

### Intake check

```
gh issue view <N> --repo <owner>/<repo> --json state,assignees,labels
gh pr list --repo <owner>/<repo> --search "<N> in:body,title" \
  --state all --json number,title,state,url
git -C <worktree> rev-parse origin/main   # useful as a baseline SHA
```

Worth reconsidering claiming the issue if:
- `state` is not `OPEN`, or
- `assignees` is non-empty and not the operator, or
- the PR search returns an open or merged PR referencing the issue.

### Pre-PR check (consider repeating this immediately before `gh pr create`)

```
gh issue view <N> --repo <owner>/<repo> --json state,assignees
gh pr list --repo <owner>/<repo> --search "<N> in:body,title" \
  --state all --json number,title,state,url
git -C <worktree> fetch -q origin
git -C <worktree> rev-parse origin/main   # compare to intake baseline SHA
```

Worth pausing before opening the PR if:
- the issue closed or was reassigned since intake, or
- a competing PR now references the issue, or
- `origin/main` moved in a way that conflicts with the change (rebasing and
  re-running checks against the new base is one option).

A moved `origin/main` that does not conflict is not necessarily a reason to
stop; re-running the affected suite against the new base first is worth
considering before opening the PR.

## Baseline differential diagnosis (operator-run, advisory)

A suggested approach for deciding whether a red test in the affected suite
is caused by the candidate change or was already failing on `origin/main`.
The operator runs and interprets this; it is not a check this skill
performs or enforces.

### Suggested procedure

1. Note the exact failing test name(s) and the command that produced them
   on the candidate branch, with output and exit code.
2. Get a pristine base checkout: check out a fresh worktree of `origin/main`
   at the same SHA recorded at intake, with no candidate changes applied.
3. Run the identical command against that pristine base.
4. Compare:
   - Same test, same failure mode on base — worth treating as pre-existing,
     with the base SHA and output noted as evidence; not necessarily
     something to fix in this PR unless it is in scope.
   - Passes on base, fails on candidate — worth treating as
     candidate-caused and blocking.
   - Different failure mode on base than on candidate — safer to treat as
     candidate-caused rather than assume it is the same defect.

### Evidence worth capturing

For any "pre-existing" claim, useful evidence includes:
- Test name
- Command
- Exit code
- Base SHA
- Output excerpt showing the same failure

A "pre-existing" claim without this evidence is weak; treating the failure
as still open until the comparison is actually run is the safer default.

## TUI smoke contract (operator-run, advisory)

A reversible, operator-run manual check for TUI-visible model-selection
state. This is a suggested smoke test, not an automated suite and not
something this skill runs — the operator performs it and restores state
afterward.

### What this tests

Whether the TUI's visible "current model" indicator updates correctly when
the human switches models through the TUI's own controls.

### What this does NOT test (do not conflate)

- **Pinned model**: a persisted user preference that survives restarts.
  This contract does not exercise pinning or unpinning.
- **Applied model**: the model actually bound to the running session after
  a switch takes effect (may lag one render cycle behind "current").
- **Current model**: the value the TUI displays as active right now. This
  contract only checks that this displayed value changes when expected —
  not that it matches "applied" or "pinned" at every intermediate frame.

Naming these separately matters: a failure to distinguish them has
previously produced false "it's broken" reports when the real behavior was
correct but appeared on a different render frame than expected.

### Procedure

1. Record the current displayed model value (the "current" indicator).
2. Switch to a sentinel model not otherwise used in this session (a model
   ID that is clearly distinguishable from your normal models).
3. Observe the "current" indicator updates to the sentinel value.
4. Restore the original model exactly as recorded in step 1.
5. Confirm the "current" indicator shows the original value again.

### Sentinel and restore

- Choose a sentinel value that cannot be mistaken for a real working
  choice (e.g., a rarely-used model ID), so any leftover state is obvious.
- Step 4 (restore) is mandatory even if steps 1-3 pass; leaving the
  sentinel active corrupts the next session's baseline.
- If restore fails, report it explicitly — do not silently continue with
  the sentinel active.

## Output Contract

This skill executes and gates nothing. It never runs a command, labels an
issue, commits, pushes, opens or merges a PR, grants review consent, or
overrides harness workflow or its consent prompts on its own. Every action
described above remains the operator's decision, run through the operator's
own tooling.
