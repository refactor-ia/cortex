# Design: Project-Owned Evidence Command (#413)

Status: proposed specification for issue #418 (epic #413). This document defines how a
project produces one readable evidence report and how the
[Evidence-First Operating Method](evidence-first.md) reads it.

## 1. Purpose and boundaries

A project defines its own evidence command: one entry point that runs the checks the
project trusts and writes one report the human can act on without reading code.

The command and its report belong to the project. **Cortex does not run the command**,
does not schedule it and does not store its report. Cortex does not extend the QA
evidence sidecar schema for it. The report is advisory input to the evidence-first
method; it grants no approval, gate or receipt and never authorizes a merge, release or
publication.

## 2. Parts

The command has three parts. Each part either runs or is marked **not applicable** with
a reason.

| Part | Purpose | Not applicable when, for example |
| --- | --- | --- |
| Test suite | Runs the project's automated tests and static checks. | The project has no executable code yet; say so. |
| Build | Produces the artifact users run. | The change ships only skills or documentation; there is nothing to build. |
| Smoke checks | Runs the built artifact once through a read-only path. | There is no runnable artifact; say so. |

"Not applicable" is never silent: the report names the part and the reason. A part that
should run but did not is **skipped**, not "not applicable", and the report says why.

## 3. Report fields

Every report records:

| Field | Content |
| --- | --- |
| Date | When the command ran, in UTC. |
| Commit | Full commit SHA of the tree that was checked, and whether the tree had uncommitted changes. |
| Environment | Operating system, architecture and the tool versions the parts depend on. |
| Command | The exact command for each part, in the order it ran. |
| Exit status | The exit status of each part's command. |
| Output | Bounded and redacted output of each part, with truncation recorded. |
| Failed or skipped | Every failed part, skipped part and not-applicable part with its reason; "none" when there is nothing. |

## 4. Recording rules

These rules reuse the recording discipline of the `qa-no-ci` source
(`catalog/families/quality-assurance/sources/qa-no-ci.md`):

- Wait for each part to finish and capture its exit status. A background launch or a
  bounded capture alone is not evidence.
- A zero exit with "no tests to run" is **not** passing evidence. Record it as such.
- Bound the output. When output is cut, record that it was truncated and where; claims
  stay limited to the captured scope.
- Keep the report private to the project. Redact secrets and personal data before
  recording; never publish them.
- Do not summarize a failure away. A failed part keeps its exit status and its bounded
  output in the report.

## 5. How the method reads a report

Each part result becomes evidence for the claims in the claim table of
[evidence-first.md](evidence-first.md) section 5:

- The part's command is the **Test command**; its exit status and bounded output are
  the **Observed result**.
- **Observed by** names who ran the command: the agent that made the change, a reviewer
  re-run or CI. A report run only by the changing agent is labeled as such.
- A part proves only what it runs. The **Limit** names what it does not cover, so a
  passing suite can still leave a claim in Incomplete evidence.
- A failed part contradicting a claim makes that claim **Refuted**.
- A missing, skipped or not-applicable part is reported in the delivery's "Failed or
  not done" part, never hidden. A claim that depended on it is **No evidence**.

## 6. Example report: Cortex itself

Illustrative format only. Values in angle brackets are placeholders, not observed
results. The suite commands mirror `.github/workflows/ci.yml`. CI does not build the
binary, so the build part uses `go build ./cmd/cortex`; the smoke check uses the
read-only `cortex doctor` documented in [operating.md](../operating.md).

Caveat: `tests/test_community_policy.py` inventories repository files. Inside a Git
worktree, `.git` is a file and is counted, so run the policy suite from a regular clone.

```text
Evidence report
Date:        <YYYY-MM-DDTHH:MM:SSZ>
Commit:      <full-sha> (uncommitted changes: <yes|no>)
Environment: <os>/<arch>, go <version>, python <version>, PyYAML <version>
Observed by: <changing agent | reviewer re-run | CI>

Part: test suite
  $ go vet ./...                                          exit <n>
  $ go test ./...                                         exit <n>
  $ go test -race ./...                                   exit <n>
  $ python -m unittest tests.test_community_policy        exit <n>
  $ python -m unittest tests.test_github_policy           exit <n>
  $ python -m unittest tests.test_runtime_adapter_contract exit <n>
  Output: <bounded, redacted output per command>
  Truncated: <no | yes, after <limit> lines of <command>>

Part: build
  $ go build ./cmd/cortex                                 exit <n>
  Output: <bounded, redacted output>
  Truncated: <no | yes, ...>
  (Not applicable for a skill-only or documentation-only change: nothing to build.)

Part: smoke checks
  $ ./cortex doctor                                       exit <n>
  Output: <bounded, redacted output>
  Truncated: <no | yes, ...>
  Note: doctor exits non-zero when an install cannot proceed; record the exit as observed.

Failed or skipped:
  <part and command, exit status, reason>   or   none
```

For a skill-only change, the build and smoke parts are reported as not applicable with
the reason "no Go code changed"; the test suite still runs, because the policy and
adapter contracts cover catalog and skill assets.
