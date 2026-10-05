# Runtime adapter contract

This reference defines the declarative safety rules for Cortex runtime adapters. It
explains expected outcomes without implementing runtime operations.

## Contract source

[`runtime-adapter-contract.yaml`](runtime-adapter-contract.yaml) is the machine-readable
contract. Cortex owns only Cortex-owned material and preserves user-owned and unrelated
configuration.

## Runtime outcomes

| Condition | Required result |
| --- | --- |
| Compatible runtime is present | Include every present compatible runtime in one all-or-nothing transaction. |
| Runtime is absent | Warn, do not install it, and continue. |
| Adapter is known incompatible | Skip and report only that adapter; do not touch it. |
| Runtime version is unknown | Warn and report the uncertainty. |
| Present runtime version is uncertified | Admit it by default, with no operator opt-in. Include every present uncertified runtime in the same all-or-nothing transaction and disclose that the admission is not certified. |
| No runtime is admitted | Report the result without installing, blocking ordinary work, or changing unrelated configuration. |

## Safe mutations

A runtime operation must use a verifiable backup, a transactional write, and an exact
read-back before reporting success. If the transaction cannot complete in-process,
reverse rollback restores it in reverse order. Uninstall removes Cortex-owned material
only; shared Pi actor state has the fail-closed limitation below.

## Pi actor-guidance updates

Current source builds support explicit single-runtime updates of existing Pi actors
with external Gentle CodeGraph guidance. This is not a native runtime certification
claim. The [README usage](../README.md#update-pi-actors-with-external-guidance) shows the
commands; `cortex update --help` describes the flags.

### Consent and preservation

- `cortex update --runtime pi --catalog <dir> [--apply] [--adopt-guidance]`
  defaults to a read-only plan. `<dir>` must contain `catalog.json`; only `--apply`
  authorizes writes. This syntax is distinct from the bare, grouped `cortex update`.
- Legacy guidance on actors recorded in v2 state requires explicit, Pi-only
  `--adopt-guidance`, even to plan its adoption. Successful apply registers v3 state.
  Verified registered v3 updates need no repeated consent.
- Updates replace only the canonical Cortex actor portion, preserving the external
  suffix byte-for-byte and native `agents/cortex-*.md` paths. They do not relocate
  actors or claim ownership of external text. Consent does not authenticate that text.

### Identity and fail-closed checks

V3 actor records separate `canonicalSha256` (the trusted catalog-rendered Cortex
actor) from `sha256` (the entire effective actor artifact, including external guidance).
The effective digest identifies file bytes, **not** a runtime-assembled prompt.
Admission checks both identities against observed bytes and the independent catalog
binding; markers and schema validity alone are not proof of current guidance.

For a supported marked legacy suffix, reconstruction appends **exactly one LF** to
its parsed canonical prefix and must match the recorded canonical hash. It does not
try arbitrary historical endings or whitespace variants. Unmarked actors instead
compare exact bytes, with no line-ending normalization.

Unknown or tampered identities, malformed markers, stale registered effective bytes,
unsafe paths/modes and actor shadows fail closed. Apply rechecks captured evidence
before mutation and verifies exact readback. `--adopt-guidance` cannot override drift.

### Removal and recovery limits

Any validated v3 Pi state blocks the **whole grouped uninstall**, even if actors are
unmarked or stale. No runtime roots, files or state change. Recorded actor paths are
diagnostics only: the schema neither proves current external guidance nor grants
removal authority. External-guidance-preserving uninstall is not supported.

Composed updates support in-process rollback. Crash/restart recovery of composed
state is unsupported; no rollback command provides that recovery.

## Projection results

| Result | Meaning |
| --- | --- |
| `exact` | The runtime represents the Cortex-owned selection without translation. |
| `translated` | The runtime uses a disclosed equivalent representation. |
| `unrepresentable` | Cortex leaves the runtime untouched rather than misrepresenting it. |

## Boundary

This is a contract reference only. It does not install runtimes, modify user-owned
configuration, or turn Cortex into a prerequisite for ordinary work.
