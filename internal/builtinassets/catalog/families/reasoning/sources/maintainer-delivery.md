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

- Read the destination repository's contributor guidance, project instructions,
  PR template, CI and branch protection: derive its issue policy, size budget,
  pre-push checks, review and merge requirements, not this skill's defaults.
- Identify the actual default branch, actual PR base (which may be a stack
  parent), and corresponding remote. Use `<remote>/<base>` below for that
  resolved base, not an assumed branch name. Record its current SHA; consider
  a fresh worktree per issue. Ask the operator about unresolved policy.

## 1. Take the issue (about 2 minutes)

- Race check: inspect issue state, assignees and open or merged PR references;
  note the resolved base SHA. Competing PR found: reconsider scope with the
  operator. See "Race checks" below; intended stack parents are not competitors.
- Follow the repository's issue policy, including any required approval or
  labels, and consider a short claim comment with scope and out-of-scope.
- Read branch, commit and title conventions from current repository guidance
  and recent merged PRs; use its PR template rather than a fixed syntax.

## 2. Kickoff prompt

Adapt and send if the operator chooses:

```
Fix <repo>#<N>. Expected behavior: <exact>. Testing: <applicable policy>.
Out of scope: <list>. Branch: <repository convention>. Checks: <required commands>.
Stop before commit and show the diff.
```

Scope growth is the main cause of slow merges. Use the repository's size budget.
If the repository has no size budget, consider about 150 changed lines as an
advisory fallback, not a gate or a fixed file-count limit. Discuss splitting or
re-scoping with the operator when review load grows.

## 3. Checks

- Focused test red then green when applicable under the testing policy;
  run the affected suite chosen for the change.
- Use the repository's required pre-push checks from its guidance and CI;
  report unavailable checks and failures rather than assume a green first CI run.
- Red suite: compare against the pristine recorded base before calling anything
  pre-existing. See "Baseline differential diagnosis" below. Never call red
  green.

## 4. Review and PR

- Follow the repository's review requirements; tooling and consent remain the
  operator's, with no model or harness-specific prerequisite from this skill.
- Consider committing only the change (no task or scratch files), following
  the repository's commit conventions and publication rules.
- Before pushing or opening a PR, consult the repository's branch protection
  and push policy. Repeat the race check, use the repo template and accepted
  closing keyword when this PR completes the issue (e.g. `Fixes #N`), with
  verification (command, result, SHA), advisories and out-of-scope. Use
  `Refs #N` instead only for a deliberately partial or WIP PR that does not
  close the issue on its own (e.g. one link in a stacked chain).

## 5. While waiting: next issue

CI, bots, and reviews are wait time, not work. Start the next issue in another
worktree. Keep one line per PR in flight so switching costs nothing:

```
<repo>#<PR>  <branch>  <step: review|ci|feedback|ready>  <next action>
```

## 6. Feedback

Collect all review comments (humans and bots) and answer in one batch: one
correction commit, one re-check, one reply. Target: same day for review and
feedback turnaround.

## 7. Merge

Use the repository's merge requirements, permissions, required checks, reviews
and sensitive-area ownership to determine readiness; this skill grants no
approval or self-merge permission. Right before merging: repeat the race check,
confirm issue state and intended scope, and check for conflicting base movement.
The operator decides whether to proceed or seek another maintainer's review.

## Stacked PR chains

Stacked chains are a supported, recommended way to land a large change as
several reviewable PRs instead of one oversized one: split scope into
ordered slices, each PR based on the previous slice's branch. Do not
discourage stacking to fit a budget; it is the preferred alternative to a
single oversized PR where the repository supports it.

Landing a chain:
- Merge parents before children, in order.
- After a parent lands, confirm the child's intended base and adapt to the
  repository's merge strategy. A squash or rebase merge may leave parent commits
  in the child: reconcile ancestry and inspect the child-only diff, not merely
  its target branch. Choose a repository-supported update with the operator
  (no force push assumed), resolve conflicts and re-run checks before continuing.
- After the base changes, recompute derived values (catalog fingerprints,
  identities, generated counts and inventories) from the tests rather than
  blindly taking either side of a git conflict on a generated or derived file.

## Measure

Time from taking the issue to merge, split into active work and waiting (CI,
bots, humans). Also PRs in flight. A slow merge caused by waiting is fixed by
step 5, not by skipping checks.

---

## Race checks (operator-run, advisory)

Example commands the operator can run at intake (before claiming an issue)
and again immediately before opening a PR. Repeat before merging, with the
actual PR base and current issue/PR state. These are suggestions, not actions
this skill takes — the operator decides whether and when to run them, and
what to do with the results. Fill placeholders from the destination repository.

### Intake check

```
gh issue view <N> --repo <owner>/<repo> --json state,assignees,labels
gh pr list --repo <owner>/<repo> --search "<N> in:body,title" \
  --state all --json number,title,state,url
git -C <worktree> rev-parse <remote>/<base>   # useful as a baseline SHA
```

Worth reconsidering claiming the issue if:
- `state` is not `OPEN`, or
- `assignees` is non-empty and not the operator, or
- the PR search returns an open or merged PR referencing the issue.

Inspect references for competition versus intended intermediate slices; a
search match alone does not establish that the issue is already completed.

### Pre-PR check (consider repeating this immediately before `gh pr create`)

```
gh issue view <N> --repo <owner>/<repo> --json state,assignees
gh pr list --repo <owner>/<repo> --search "<N> in:body,title" \
  --state all --json number,title,state,url
git -C <worktree> fetch -q <remote>
git -C <worktree> rev-parse <remote>/<base>   # compare to intake baseline SHA
```

Worth pausing before opening the PR if:
- the issue closed or was reassigned since intake, or
- a competing PR now references the issue, or
- the resolved base moved in a way that conflicts with the change (updating
  and re-running checks against the new base is one option).

A moved base that does not conflict is not necessarily a reason to stop;
re-running the affected suite against the new base first is worth considering
before opening the PR. Record the new base SHA when refreshing evidence.

## Baseline differential diagnosis (operator-run, advisory)

A suggested approach for deciding whether a red test in the affected suite
is caused by the candidate change or was already failing on the recorded base.
The operator runs and interprets this; it is not a check this skill
performs or enforces.

### Suggested procedure

1. Note the exact failing test name(s) and the command that produced them
   on the candidate branch, with output and exit code.
2. Get a pristine base checkout: check out a fresh worktree of the resolved base
   at the same SHA recorded for the comparison, with no candidate changes applied.
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

## Output Contract

This skill executes and gates nothing. It never runs a command, labels an
issue, commits, pushes, opens or merges a PR, grants review consent, or
overrides harness workflow or its consent prompts on its own. Every action
described above remains the operator's decision, run through the operator's
own tooling.
