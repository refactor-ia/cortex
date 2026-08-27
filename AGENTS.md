# Cortex repository rules

Architecture documentation is the authority for Cortex product boundaries. Keep repository artifacts and public communication in English; private maintainer conversation may use another language.

- Do not store, publish, or repeat secrets or personal data.
- Never publish `.env*`, `.ai/**`, or `.atl/**`.
- Do not add generation or tool attribution to Git or GitHub artifacts.
- Use issue-first work: pull requests reference `Refs #N` or `Fixes #N`.
- Do not push directly to `main`; GitHub permissions and branch protection are the authorization authority.
- Keep pull requests below the 400-line review budget unless an approved exception is documented.
- Use conventional commits.
- User configuration remains user-owned; Cortex must not take it over without explicit ownership.
- For JavaScript or TypeScript tooling, use `pnpm` rather than `npm`.
- Cortex has no Git, SDD, TDD, or review authority.
- `git-specialist` is permanently excluded from this repository.
