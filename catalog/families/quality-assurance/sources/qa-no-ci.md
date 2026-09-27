# QA Without CI

<!-- cortex-qa:no-ci-report-only -->
<!-- cortex-qa:cortex-qa-run-report-only -->
<!-- cortex-qa:claude-pi-background-argv -->
<!-- cortex-qa:stdin-never-accepted -->
<!-- cortex-qa:no-inference -->
<!-- cortex-qa:no-paid-call-without-authorization -->
<!-- cortex-qa:no-retries -->
<!-- cortex-qa:authorized-tools-disposable-worktree -->
<!-- cortex-qa:no-overbroad-results -->
<!-- cortex-qa:guidance-not-launcher -->

## Activation Contract
Read this skill to plan a human-directed, bounded QA request without invoking anything. Before execution, obtain explicit authorization for the selected backend model call; reading or activating this skill alone does not authorize execution. `cortex qa run` invokes that selected backend model through the selected backend CLI to assess the request and evidence; report-only means it does not execute CI or tests or make an integrated product change.

## Hard Rules
- Never use `cortex qa run` to execute tests; test execution needs separately authorized tools and a confirmed disposable worktree.
- Never make a paid provider or model call without explicit authorization; never retry failed, timed-out, or ambiguous calls.
- Never infer behavior for an untested CLI, model, or skill or report beyond the selected scope.

## Decision Gates
- For a Claude Code → Pi background check, before invocation validate the user-selected provider/model, an installed Cortex QA skill selected with `--skill`, and a nonempty prompt wholly in argv; invoke only after those checks and explicit human authorization.
- Require a finite timeout available on this host. GNU `timeout` is not stock macOS; stop if the required timeout command is unavailable.
- In this exact branch, invoke `pi -p` and close Pi's unused standard input with `</dev/null` only because the prompt is wholly in argv; never use it when the prompt comes through standard input or generalize this guidance to another CLI or model.

## Execution Steps
- Use only authorized tools in a confirmed disposable worktree.
- Wait for completion and capture the exit status, selected provider/model, selected skill, command scope, and bounded output and error evidence; record truncation, keep evidence private, never publish secrets, and limit claims to the captured scope.
- A background launch or bounded capture alone does not validate QA; record truncation and limit claims to the captured scope.
- Treat a zero exit with `no tests to run` as no passing test evidence.

## Output Contract
Report only observations and evidence for the selected request, command, model, skill, or test; state uncertainty and scope explicitly. Reading or installing this skill is not an automatic launcher and must never start `cortex qa run`, `pi`, a provider call, or any background process.
