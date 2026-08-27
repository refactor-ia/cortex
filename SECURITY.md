# Security Policy

## Report a vulnerability privately

Do not open a public issue for a suspected vulnerability. Report it through one of these private channels:

- Email [security@refactoria.dev](mailto:security@refactoria.dev).
- Use [GitHub Private Vulnerability Reporting](https://github.com/refactor-ia/cortex/security/advisories/new).

If a real secret has been exposed, its owner must revoke or rotate it immediately. Do not retransmit the secret in email, an advisory, an issue, or any other report.

## Include safe, actionable details

Provide only the information needed to assess and reproduce the report:

- A bounded reproduction or proof of concept.
- The potential impact and affected component or version.
- Safe evidence, such as sanitized logs, outputs, or screenshots.
- Any mitigation or workaround already known.

Do not include secrets, credentials, tokens, private keys, real connection strings, or unnecessary personal data.

## Support status

Cortex is transitioning to a target architecture that is not yet a stable released contract. A supported-version matrix will be published with the first verified release. Until then, reports are still welcome, but the project cannot represent unverified transition or legacy material as supported.

## Coordinated disclosure

RefactorIA will assess reports privately, coordinate with affected parties when appropriate, and work toward a responsible disclosure. The disclosure plan depends on the report's impact, reproducibility, and affected users or components. This policy does not promise a response time, a bounty, or legal safe-harbor terms.
