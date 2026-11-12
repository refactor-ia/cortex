# Design: Evidence-First Operating Method (#413)

Status: proposed specification for issue #414 (epic #413). This document defines the
method, its evidence states and the agent delivery contract. The capability that
carries it to supported runtimes is tracked in #415 and is out of scope here.

## 1. Purpose and boundaries

The method describes how a human works with agents without reading code. The human
directs, asks for proof and does QA on what they requested. Agents report everything,
including what failed, what they did not do and what they think is a mistake. Evidence
and disclosure replace the diff as the interface between the human and the work.

The method is advisory. It grants no approvals, gates or receipts, and it never
authorizes a merge, release or publication. Those stay human decisions under repository
policy. It is runtime-neutral and stateless: it does not depend on any harness, review
system or workflow engine.

## 2. Principles

1. **No decision without evidence.** When there is no evidence, that is the finding.
2. **Claim, relevant test, observed result, limit.**
   - Claim: something concrete said about the system.
   - Relevant test: an execution that could prove the claim false.
   - Observed result: what happened when it ran, without over-interpreting it.
   - Limit: what that test does not prove.
   A green suite proves the cases it runs, nothing more.
3. **Every claim ends in exactly one of four states** (section 3).
4. **Nothing is hidden.** Failures, skipped steps, blockers and parts of the request
   that were not done are reported as prominently as successes. When there are none,
   the agent says "none" explicitly.
5. **Warn before acting.** When a request is wrong, unsafe or carries serious
   consequences, the agent says so before doing it, explains why and proposes a better
   path. The human decides.

## 3. Evidence states

| State | Means | Action |
| --- | --- | --- |
| Verified | A relevant test exists, ran, and its result supports the claim. | Use it to decide. |
| Incomplete evidence | Tests exist and pass, but do not cover the whole claim. | Name the gap. |
| No evidence | Nothing checks the claim today. | That is the finding. |
| Refuted | A relevant test ran and its result contradicts the claim. | Treat the claim as false. |

Tie-break: when any relevant test contradicts the claim, the state is Refuted,
whatever other tests report. Refuted covers the existing "Ineffective" verdict of
`maintainer-review`, where a controlled reproduction shows a claimed fix fails.

Each claim also records **who observed the result**: the agent that made the change, a
reviewer re-run, or CI. A result observed only by the agent that made the change is
labeled as such.

## 4. Agent delivery contract

Every delivery returns six parts.

| Part | Content |
| --- | --- |
| Changed | What changed, in one plain sentence. |
| Tested how | Exact command, bounded and redacted output, and who observed it. |
| Not tested | The limit, stated by the agent. |
| Side effects | What else the change touches, such as shared code or other consumers, and the proof that their behavior did not change. |
| Failed or not done | Every failed check, skipped step, blocker and part of the request not completed, with the reason; "none" when there is nothing. |
| Warnings | Any risk or consequence the human should know before accepting. |

A delivery missing any part is **Unverified**, with the missing part named and the
next action stated. Unverified is a finding, not a block. A delivery that omits a
failure later observed is reported as undisclosed, never as Verified.

**Side effects close the gap that reading diffs used to cover.** Without reading code,
the human still learns what the agent touched beyond the request. A typical proof runs
the same command against the previous and the new version and compares its output,
error output and exit code.

## 5. Claims from acceptance criteria

The claims come from the linked issue's acceptance criteria, not only from the agent.
Each criterion maps to at least one claim. A criterion with no claim is reported as a
No evidence row. This keeps an agent from proving only what it chose to prove; it does
not prove that the criteria themselves are complete.

A claim table has one row per claim:

| Claim | Test command | Observed result | Limit | State | Observed by |
| --- | --- | --- | --- | --- | --- |

## 6. Worked example

Claim: "the budget is saved".

| Variant | Test | Observed result | Limit | State |
| --- | --- | --- | --- | --- |
| Without restart | Create a budget, then read it back. | The budget is returned. | Does not rule out that it only lives in memory. | Incomplete evidence |
| With restart | Create a budget, restart the app, read it back. | The budget is returned after the restart. | Does not prove behavior under concurrent writes. | Verified |
| Broken storage | Create a budget, restart the app, read it back. | Not found after the restart. | None needed; the claim is contradicted. | Refuted |
| Concurrent writes | None: no test writes the same budget from two sessions. | Nothing ran. | Nothing about concurrent writes is known. | No evidence |

A delivery for the same change, with all six parts:

| Part | Example |
| --- | --- |
| Changed | Budgets are now stored in the database instead of in memory. |
| Tested how | `pnpm test budgets/persistence`: 4 passed, exit 0, including create, restart and read back. Observed by: the agent that made the change. |
| Not tested | Concurrent writes to the same budget; migration of budgets created before this change. |
| Side effects | The budget list endpoint shares the storage module. `pnpm test budgets/list` ran against the previous and the new version with identical output and exit 0. |
| Failed or not done | The import-from-CSV path was not migrated; it still writes to memory and is tracked as a follow-up. No check failed. |
| Warnings | Budgets created before this change are lost on the first restart unless the migration runs first. |

## 7. Anti-patterns

- **Happy-path-only tests.** An access test that only checks the allowed user does not
  prove that others are denied.
- **Green CI as proof.** CI proves the cases it runs. A zero exit with "no tests to run"
  is no passing evidence.
- **Invented numbers.** Any metric, percentage or confidence score without a command
  that produced it.
- **Silent failures.** Leaving a failed or skipped check out of the delivery because
  the rest passed.
- **Doing first, warning later.** Carrying out a harmful request and mentioning the
  consequence only afterwards.

## 8. Relation to Gentle AI

Gentle AI reviews and verifies a frozen local candidate inside its own harness. This
method governs how the human operates and what any agent must disclose to them, in any
supported runtime. The overlap evaluation for #415 found partial overlap and no
material overlap:

| This method | Gentle AI practice | Difference |
| --- | --- | --- |
| Four evidence states | Verify returns a verdict per spec item of an ODD feature document. | States here apply to claims from issue acceptance criteria. |
| Tested how | Workers report `<command>: <observed result>`. | Adds bounded output and who observed it. |
| Side effects | ODD runs commands sharing changed code against the previous and the new code. | Here it is a delivery part, not a test rule. |
| Failed or not done | ODD close reports every failed or pending check. | Requires an explicit "none". |
| Not tested, Warnings, Unverified | No equivalent found. | Owned by this method. |

Gentle AI outputs may be used as input evidence with their attribution, never
reinterpreted. Cortex keeps no runtime dependency on Gentle AI.

## 9. Provenance and license

The method comes from the maintainer's own engineering practice, presented publicly in
streams and talks, and is contributed to Cortex by the maintainer. It is published
under the Cortex content license (CC BY-SA 4.0; see [LICENSE-CONTENT.md](../../LICENSE-CONTENT.md)).
