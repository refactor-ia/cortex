# Contributing to Cortex

Thank you for helping improve Cortex. Contributions begin with a clear public problem statement and proceed through focused reviewable work.

## Community and reporting

Read the [Code of Conduct](CODE_OF_CONDUCT.md), [Security Policy](SECURITY.md), [Governance](GOVERNANCE.md), and [Support guide](SUPPORT.md). Report vulnerabilities privately under the Security Policy and conduct incidents confidentially under the Code of Conduct.

## Issue first

Use the [issue chooser](https://github.com/refactor-ia/cortex/issues/new/choose) for bugs, scoped improvements, and documentation problems. Every pull request must reference the issue that explains its purpose:

```text
Refs #123
Fixes #123
```

Discuss substantial changes before implementation so scope and expected outcomes are reviewable.

## Pull requests

- Use a dedicated branch and do not push directly to `main`.
- Keep each pull request focused and below 400 changed lines whenever possible.
- Split broader work into follow-up pull requests rather than mixing unrelated changes.
- Complete the pull request template and include relevant verification evidence.
- Required checks and review must pass before merge.
- GitHub repository permissions and branch protection are the authorization authority for pushing and merging.

## Privacy and repository hygiene

Do not commit, attach, paste, or publish secrets, credentials, tokens, private keys, real connection strings, or unnecessary personal data.

Never publish `.env`, `.env.*`, `.ai/**`, or `.atl/**`. Do not add local caches, databases, logs, editor state, or machine-specific configuration. If a secret is exposed, revoke or rotate it before continuing.

## Public language

Repository-facing artifacts and public collaboration are in English, including documentation, source comments, issues, pull requests, reviews, commits, and release notes. Private maintainer conversation may use another language.

## Conventional commits

Use conventional commit messages, for example:

```text
feat: add capability manifest validation
```
