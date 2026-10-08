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
| `cortex install` | Yes, transactional | Configures every compatible present runtime in one all-or-nothing transaction. |
| `cortex update --runtime <id> --catalog <dir>` | Only with `--apply` | Plans (default) or applies (with `--apply`) a single-runtime update. |
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

**Exit code**: 0 means an install can proceed; a non-zero exit means uncertainty — read the report, do not retry blindly. If a runtime version cannot be identified at all, Cortex refuses to write to that runtime; report this to the user rather than editing configuration to force it.

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

## Running update

`update` is scoped to one runtime and needs the catalog directory:

```bash
cortex update --runtime pi --catalog ./catalog          # read-only plan
cortex update --runtime pi --catalog ./catalog --apply  # the plan, applied
```

Default mode touches nothing — run the plan first, read it, then decide. `--apply` writes only inside that runtime's Cortex-owned root. On Pi, if legacy externally-modified guidance is detected, the plan asks for explicit consent with `--adopt-guidance`: that is a one-time adoption of external bytes (they are preserved, not overwritten), not a drift override, and drift is always refused.

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

## Where to read more

- [Architecture overview](architecture/overview.md) — the target product contract (authority).
- [Runtime adapter contract](../references/runtime-adapter-contract.md) — identity checks, actor-guidance updates, and supported legacy endings.
- [Verification and release evidence](verification.md) — how to verify an archive and what was tested.
- [`AGENTS.md`](../AGENTS.md) — the contract for agents contributing to this repository (a different role from operating it).
