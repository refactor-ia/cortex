# Operating Cortex: a guide for agents acting on a user's behalf

You are an agent setting up or maintaining Cortex on someone's machine, at their request. This guide tells you what you can do, what the output means, and what Cortex will refuse — so you can act correctly without reading source code. It does not replace the [architecture overview](architecture/overview.md), which stays the authority for the product contract; this page links rather than paraphrases it.

## What Cortex is, in one paragraph

Cortex writes a curated set of agent capabilities (skills, and on Pi also agents) into the AI coding runtimes present on this machine — Pi, OpenCode, Claude Code. It owns only the files it writes, applies changes transactionally, and removes exactly what it wrote on uninstall. It is not a harness: it holds no Git, SDD, TDD, or review authority, and it never sits between the user and their runtimes.

## Ground rules — read before running anything

1. **Cortex never installs a runtime.** If a runtime is absent, Cortex warns and continues with the ones present. Do not try to make it install Pi, OpenCode, or Claude Code; that is deliberately impossible.
2. **Cortex never takes over user configuration.** It writes only its own material. If a file it needs to manage shows signs of external modification, Cortex refuses instead of overwriting — that refusal is a feature, not an error to work around.
3. **Every write is opt-in or transactional.** `doctor` only reads. `install` and the grouped `uninstall` are the only commands that write without a flag, and both report exactly what they touched. `update` plans read-only by default and writes only with `--apply`.
4. **Do not work around refusals.** `unrepresentable`, `known_incompatible`, and `ownership_conflict` are Cortex failing closed. The correct response is to report them to the user, not to defeat them.

## The command set

| Command | Writes? | What it does |
| --- | --- | --- |
| `cortex doctor` | Never | Probes present runtimes, reports compatibility, and tells you whether an install can proceed. |
| `cortex install` | Yes, transactional | Configures every compatible present runtime in one all-or-nothing transaction. `--target-dir <dir>` projects the catalog into an existing directory instead. |
| `cortex update --runtime <id> --catalog <dir>` | Only with `--apply` | Plans (default) or applies (with `--apply`) a single-runtime update. |
| `cortex doctor --target-dir <dir>` | Never | Reports a directory target's presence, writability, and ownership. |
| `cortex uninstall` | Yes, conservative | Removes exactly the verified Cortex-owned material; refuses on conflict. |
| `cortex qa run --role <role> --request <file> --catalog <dir>` | Never | Evaluates a bounded request (and optional evidence file) and writes a QA report. Report-only; it does not execute tests. |

`cortex --version` reports the running binary's build identity and embedded catalog ID.

## Reading `doctor` output

Each line is one runtime:

```
runtime=pi presence=present compatibility=compatible action=configure touch=denied
```

| Field | Meaning |
| --- | --- |
| `presence` | `present` (found on the machine) or `absent` (nothing to do; Cortex will not install it). |
| `compatibility` | `compatible` (verified version), `uncertified` (recognised runtime, no verified evidence for that exact version — still configured, with a disclosed warning), or `known_incompatible` (recorded as incompatible; that adapter is skipped, others still apply). |
| `action` | What install would do: `configure`, or a skip/refusal for incompatible or unidentifiable targets. |
| `touch` | Always `denied` in doctor: doctor never writes. |

**Exit code**: 0 reports successful runtime/readiness probing, not verified asset ownership, successful model execution or certification. A later install/update can still refuse a conflict. A non-zero exit means uncertainty — read the report, do not retry blindly. If a runtime version cannot be identified at all, Cortex refuses to write to that runtime; report this to the user rather than editing configuration to force it.

## Running install

`cortex install` requires no flags. The summary line reports counts:

```
operation=install status=completed touch=applied create=33 replace=0 remove=0 unchanged=0 preserve=0 ...
```

- `status=completed touch=applied` — the transaction applied and was read back. Running install again is a no-op reported honestly as `create=0 unchanged=33`.
- `status=not_applied reason=compatibility_uncertified touch=denied` — no compatible runtime was found; nothing was written.
- `status=not_applied reason=projection_unrepresentable touch=denied` — a runtime exists but cannot represent a capability honestly; Cortex refused rather than approximating. Report it; do not work around it.
- `status=not_applied reason=ownership_conflict touch=denied` (exit 3) — a target file exists without verified Cortex ownership. Someone else wrote there. Stop and ask the user; never delete or rewrite foreign files to clear the path.
- `warning=uncertified_admission runtimes=N certification=not_certified` — the transaction configured N runtimes whose exact version has no verified evidence. This is disclosed non-certified admission, not a failure; the user may accept it once they know.

## Directory targets

`cortex install --target-dir <dir>` projects the catalog as plain `SKILL.md` files into an
existing directory, with the same transactional guarantees as a runtime install: manifest,
ownership, backup, rollback, and readback. There is no host to probe, so there is no version
certification step; the path must already exist and be a directory.

The output follows the [Agent Skills](https://agentskills.io/) specification: each capability
lands at `skills/cortex-<name>/SKILL.md` with `name`, `description`, `license`, and
`metadata.cortex-*` frontmatter. Any host that reads `SKILL.md` files from a directory —
PowerShell-based agent surfaces included — consumes that layout unchanged. QA actors project
the same way as self-contained skills; a directory target never runs `qa run` backends and
adds no QA route.

```bash
cortex install --target-dir ./agent-skills

operation=install status=completed touch=applied create=33 ...
runtime=directory presence=present compatibility=compatible action=configure touch=applied
```

- `error=invalid_target` (exit 64) — the path is missing, relative, or not a directory.
- Update and uninstall work on the recorded ownership:
  `cortex update --target-dir <dir> --catalog ./catalog [--apply]` (a target with no prior
  state is refused with `reason=not_installed` instead of becoming a first install), and
  `cortex uninstall --target-dir <dir>` removes exactly the owned files; foreign files stay.
- `cortex doctor --target-dir <dir>` reports `presence`, `writability`, and `ownership`
  (`unowned`, `owned`, `invalid`) for the directory; an unwritable directory or an unreadable
  prior state exits with the same conflict code install would hit.

## Running update

`update` is scoped to one runtime and needs the catalog directory:

```bash
cortex update --runtime pi --catalog ./catalog          # read-only plan
cortex update --runtime pi --catalog ./catalog --apply  # the plan, applied
```

Default mode touches nothing — run the plan first, read it, then decide. `--apply` writes only inside that runtime's Cortex-owned root. On Pi, if legacy externally-modified guidance is detected, the plan asks for explicit consent with `--adopt-guidance`: that is a one-time adoption of external bytes (they are preserved, not overwritten), not a drift override, and drift is always refused.

## Upgrade to alpha20

Use the [published alpha20 archive and checksum procedure](verification.md#verify-an-archive) for the machine's platform. Keep the extracted binary, catalog and licenses together in a versioned directory. Do not replace user model routes or runtime configuration with release defaults.

1. Run the extracted `./cortex --version` and check the exact release identity in [delivered evidence](verification.md#alpha20-delivered-evidence).
2. Record the currently selected executable (`command -v cortex`) and its version. Back up that executable before replacement; stop if it is not the location you intended to manage. Do not change shell PATH or overwrite a different installation implicitly.
3. For each present runtime, run the read-only plan with an **absolute** path to the newly extracted catalog:

   ```bash
   ./cortex update --runtime pi --catalog "$(pwd)/catalog"
   # Only after an admitted plan:
   ./cortex update --runtime pi --catalog "$(pwd)/catalog" --apply
   ```

   Repeat for `opencode` and `claude-code`. Per-runtime updates are separate transactions: success in one does not make the entire machine complete. For a first installation, use `install`, not `update`.
4. Retain the reported Cortex transaction backup IDs. Read back the owned capability files through the ownership manifest; unrelated bare skill directories can coexist with Cortex-prefixed directories. Do not delete a duplicate-looking directory to make verification pass.
5. Replace the intended executable atomically from the verified release, retaining its backup. Check the selected `cortex --version` again. `doctor` is a separate readiness probe, not proof that each update applied or that model calls work.

### Pi guidance refusal and explicit adoption

`status=conflict reason=actor_guidance_unverified` means the Pi update has not verified existing guidance ownership. That runtime is **not updated**, even if other runtimes or the executable succeeded. Do not delete guidance, alter ownership records, force an overwrite, or silently add `--adopt-guidance`.

Where verified legacy external guidance is eligible, an owner may explicitly authorize conservative adoption using `--adopt-guidance` on the Pi plan and apply commands. Adoption preserves the external guidance; it does not authorize arbitrary drift or bypass other refusals. An agent must obtain that specific decision rather than infer it from a general installation request. If admission still fails, report the reason and leave Pi unchanged.

### Partial failure and recovery

Preserve command exit codes, output and the last completed transaction. A failed post-update verifier does not prove that the preceding update rolled back. Inspect the recorded ownership paths/hashes and backup IDs before continuing; do not blindly repeat already-applied work. Backups and a restored executable alone do not roll back capability files. Follow the applicable transaction recovery contract, and report a partial installation honestly when proof or authorization is unavailable.

## Running uninstall

`cortex uninstall` removes only material verified as Cortex-owned, making a backup first. Per-runtime statuses:

- `uninstall=completed remove=N` — removed exactly what it owned.
- `not_installed` — nothing of Cortex's there; nothing done.
- `conflict` / `blocked` — shared actor state or unverified ownership blocks removal; the whole grouped uninstall fails closed and nothing is removed. Report the diagnostic notes to the user instead of forcing it.

## Never do this

- Never edit runtime configuration, provider credentials, or model settings to make Cortex succeed. User configuration stays user-owned.
- Never delete, rename, or "clean up" files in a runtime's directory that Cortex does not claim.
- Never treat a zero exit from `qa run` as passing test evidence: it is a report about the request you supplied, not a test execution.
- Never run `install`/`update --apply`/`uninstall` against a machine the user did not explicitly hand you for this purpose.

## ODD roadmap workflow

Product direction is coordinated through the ODD roadmap practice. These rules bind
anyone planning, starting, or contributing work in this repository.

- The living index issue (#288) is the single source for sequencing and readiness.
  [ROADMAP.md](../ROADMAP.md) holds the static narrative; this index holds the approved
  order and the current state.
- Approved outcomes are GitHub milestones. A family or track becomes a milestone only
  through a maintainer-approved revision appended to the index; approved revisions are
  never rewritten, only appended with a new proposal marker.
- Each ODD work unit carries its own plan in its issue: scope, acceptance criteria, and
  checks. Local ODD task documents (`odd/tasks/<feature>.md`) mirror the specs verbatim
  for execution tracking and are not a substitute for the issue.
- Readiness gates contribution: an issue is contribution-ready only when its plan is
  complete and its readiness conditions are resolved. Nothing is announced as ready
  while a decision is pending.
- Ideas without an approved milestone stay un-prioritized and create no issues
  automatically. Bugs and small tasks keep the normal issue-first flow without ODD
  treatment.
- No dates and no promised versions anywhere in the workflow.

## Prior install-state schemas

Cortex classifies prior installs with schema 1 or 2. A prior install state this build
cannot classify blocks install and update by design; the refusal names itself instead of
an opaque transaction failure:

- `cortex install` reports `reason=install_state_schema_unsupported prior_schema=N supported=1,2`.
- `cortex doctor` reports `note=install_state_schema_unsupported runtime=... prior_schema=N supported=1,2 remedy=...` and exits non-zero, so its exit code stays honest about install readiness.

Operator path when blocked: adopt the prior state through a guided update where one is
offered, or archive the stale state (the `.cortex` directory and the projected files it
lists) and reinstall. Archiving means a dated backup directory outside the runtime —
never deletion.

## Configure your QA route

The QA fleet ships no models: the route policy's `nan` provider is a placeholder, and
Cortex applies no model fallback. Each owner configures their own route through a
user-owned profile file in the catalog root:

```json
{"schemaVersion":1,"defaultProfile":"my-route","profiles":{"my-route":{"routes":{"evidence-auditor":{"pi":{"provider":"nan","model":"glm5.3","effort":"high"}}}}}}
```

Location: `<catalog-root>/cortex/qa-profiles.json` (mode 0600, regular file). The
`defaultProfile` applies to every `qa run` without a `--profile` flag; pass
`--profile <id>` to select another profile for one run. The three optional flags
`--backend`, `--evidence` and `--profile` are independent: each may be supplied
once, in any order, after the required flags. When the pinned model is
absent from the runtime's effective catalog, `qa run` refuses at preflight with
`model_unavailable` and prints this setup path — no launch, no silent fallback.

A profile source that exists but cannot be read safely is refused, never silently
replaced: a catalog root reached through a symlink, a symlinked or non-regular
profile file, an oversized or malformed file, or an unreadable `defaultProfile`
all end in `profile_invalid` before anything is launched. Only a catalog with no
`cortex/qa-profiles.json` file at all uses the placeholder `nan` route.

Neither command accepts a catalog root reached through a symlink: every path
component must be a real directory, and `qa setup` refuses to create a missing
tree. This is a strict policy, not a bug to route around, because the profile and
the shadow scan have to address the same bytes the run reads. On macOS that
includes the system temporary directory: `/tmp` is a symlink to `/private/tmp`, so
a catalog under `/tmp/...` is refused until it is addressed through its canonical
`/private/tmp/...` path (`cd` into it and use `pwd -P`).


## Walk the QA fleet end to end

The showcase path (#286): from a fresh directory, install from a release, configure a
route, and receive bounded, attributable QA evidence for one request. What a verified
run looks like, and where it currently stops:

1. **Fresh install.** Download a release tarball, unpack it, and run `./cortex install`
   from an empty directory. Expect `status=completed`, per-runtime lines, and the
   `warning=uncertified_admission` disclosure. Verify readiness with `cortex doctor`:
   every runtime reports `qa_availability` for the probe role only — it is a model
   availability probe, not an admission guarantee.
2. **Configure the route.** The fleet ships no models, so pick a provider/model pair
   from the runtime's extension-free surface and run
   `cortex qa setup --catalog <abs-path>/catalog --provider <p> --model <m>`. Use an
   **absolute** catalog path: `qa run` only reads the route profile from a canonical
   absolute root (#324). `qa setup` writes `<catalog>/cortex/qa-profiles.json` with one
   route per fleet role, pi-backend scoped (#328).
3. **Run one role with evidence.** Write a short bounded `request.txt`, then:

   ```sh
   printf '[{"id":"selected-test","command":"go test ./...","exit_code":0,"output_tail":"ok example","provenance":"caller fixture","truncated":false}]' > evidence.json
   cortex qa run --role requirements-analyst --request request.txt \
     --catalog <abs-path>/catalog --backend pi --evidence evidence.json
   ```

   The sidecar must be a JSON array of execution records; the report evaluates them
   and never runs commands itself. On success the report is the only stdout output;
   the retained stream lands in `.cortex/qa-streams/` for audit.

   Cortex does not launch retries or select fallback models. Pi may retry internally
   within its single invocation. A recovered report is accepted only after matched
   failed-attempt envelopes, an ordered retry notification (up to three attempts),
   a deletion-only context edit, and a successful retry-end notification. Tools,
   duplicate results, aborted runs, incomplete recovery and final backend failures
   remain rejected; failed-attempt text is never returned as the report.

   **Evidence record schema:**

   `cortex qa run --evidence <file.json>` requires a top-level JSON array containing up to 8
   execution records, bounded at 48 KiB (49,152 bytes) total. Custom top-level shapes (such as
   `{"schema": ..., "candidate": ..., "evidence": [...]}`) are rejected with explicit attribution.

   Each record must be a JSON object admitting only the following fields:

   - `id` (string, required): unique record identifier, non-empty, trimmed, no control characters.
   - `command` (string, optional): executed command line invocation.
   - `exit_code` (integer, optional): process exit status code (e.g. `0`, `1`). Non-integer numbers or strings are rejected.
   - `output_tail` (string, optional): tail of standard output/error, bounded to 4096 bytes.
   - `provenance` (string, optional): description of origin or caller fixture source.
   - `truncated` (boolean, optional): whether captured output was truncated (`true`/`false`).

   Validation is strict: unknown fields, missing required fields, type mismatches, or duplicate
   keys fail closed with diagnostic tokens `evidence_field=<field> evidence_reason=<reason>`.
4. **Known blockers (2026-10-19 walkthrough).** No backend completes the green run yet:
   pi hits provider transport failures misreported as `normalization_failed` (#326);
   claude streams from live Claude Code 2.1.303 are rejected on informational
   `thinking_tokens` events (#327); opencode has no configurable route (#328). The
   showcase closes only when a release binary completes steps 1–3 end to end and the
   report comes back with per-role findings and minority findings preserved.

## Where to read more

- [Architecture overview](architecture/overview.md) — the target product contract (authority).
- [Runtime adapter contract](../references/runtime-adapter-contract.md) — identity checks, actor-guidance updates, and supported legacy endings.
- [Verification and release evidence](verification.md) — how to verify an archive and what was tested.
- [`AGENTS.md`](../AGENTS.md) — the contract for agents contributing to this repository (a different role from operating it).
