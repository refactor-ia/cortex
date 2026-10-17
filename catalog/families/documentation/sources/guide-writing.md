# Guide Writing

Write project documentation that a reader can act on without help. A guide exists to
move one specific reader from one specific state to another; everything else is noise.

## What counts as a guide

A guide is a single document teaching one task or decision: a walkthrough, a how-to,
an onboarding path, or a review-facing explanation. Reference tables and API listings
are not guides; keep them separate and link to them instead of restating them.

## Hard rules

- One document, one reader, one job. State both at the top in the first paragraph.
- Start from the reader's actual starting state, not from your knowledge. Name the
  prerequisites and verify them before the first step.
- Every command, flag, and expected output is copy-paste runnable and verified against
  the current release, not written from memory.
- Show expected output where a reader would otherwise guess: exit codes, counts, and
  refusal messages are part of the interface.
- No invented dates, no promised versions, no "coming soon" sections. Describe what
  exists and what does not, honestly.
- Never document a behavior you did not observe. If you cannot verify a step, write
  that it is unverified or remove it.

## Workflow

1. Define the reader, their starting state, and the end state before writing.
2. Run every step yourself in a clean environment; record observed output.
3. Write the steps from the evidence, keeping each step one verifiable action.
4. Test the document by following it literally as a newcomer would, and fix every
   place where you had to know something the document did not say.
5. Keep the document alive: when the behavior changes, the guide changes in the same
   change, or the guide says it is stale.

## Review checklist

- Could a reader finish the task using only this document?
- Does every command come from a verified run?
- Does the document state what failure looks like, not only success?
- Is anything in it a promise instead of a fact?
