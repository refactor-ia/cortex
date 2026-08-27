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
| No compatible runtime is present | Report the result without installing, blocking ordinary work, or changing unrelated configuration. |

## Safe mutations

A runtime operation must use a verifiable backup, a transactional write, and an exact
read-back before reporting success. If the transaction cannot complete, reverse rollback
restores the transaction in reverse order. Uninstall removes Cortex-owned material only.

## Projection results

| Result | Meaning |
| --- | --- |
| `exact` | The runtime represents the Cortex-owned selection without translation. |
| `translated` | The runtime uses a disclosed equivalent representation. |
| `unrepresentable` | Cortex leaves the runtime untouched rather than misrepresenting it. |

## Boundary

This is a contract reference only. It does not install runtimes, modify user-owned
configuration, or turn Cortex into a prerequisite for ordinary work.
