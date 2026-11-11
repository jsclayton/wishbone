# Wishbone

Private, self-hosted family wish lists. Members claim gifts for each other without spoiling the surprise. A Go backend with an embedded React frontend, shipped as one distroless image.

## Where truth lives

- **Behavior**: [`docs/spec.md`](docs/spec.md) is the source of truth, and [`docs/routes.md`](docs/routes.md) is its route table. Read the spec before you implement or change a feature. Use the terms in [`GLOSSARY.md`](GLOSSARY.md) in code, tests, UI copy, and docs.
- **Decisions and reasons**: [`docs/adr/`](docs/adr/README.md) (read the relevant one before you change auth, groups or visibility, or reveal) and [`docs/rationale.md`](docs/rationale.md) (read the matching entry before you revisit a product or tooling decision).
- **Code**: [`docs/architecture.md`](docs/architecture.md) says where each rule lives. [`docs/development.md`](docs/development.md) has setup, the repo layout, tests, and [releases](docs/development.md#releases).
- **Process**: [`docs/workflow.md`](docs/workflow.md) covers tickets, branches, PRs, review, merge, and parallel work.

## Commands

`just --list` shows every recipe. Use the recipes, not a bare `go build`: they copy the real frontend build into `internal/server/web/dist/`, or write the placeholder that Go needs to compile.

```bash
just install                 # JS dependencies and git hooks; add `just install-playwright` once for e2e
just dev-backend             # Go server on :8080; pair with `just dev-frontend` on :3000
just lint                    # every linter and the type checker; `just fmt` formats Go
just test                    # Go with -race, then Vitest. Slow: internal/server alone takes 6 to 11 minutes under -race
go test ./internal/server -run TestName                          # one Go test
pnpm --filter web exec vitest run src/Claim.test.tsx             # one web test file
WISHBONE_PORT=9234 just test-e2e                                 # all e2e specs, on a random port
just build-e2e && WISHBONE_PORT=9234 pnpm --filter web test:e2e e2e/gifts.spec.ts   # one e2e spec, random port
just migration <name>                                            # a new migration
go test ./internal/server -run OpenAPIDocument -update           # refresh the OpenAPI copy after a route change
```

## Rules that break CI or hooks

- Run JS tooling through **pnpm**, never npm. Dependency install scripts are blocked unless `pnpm-workspace.yaml` allows them (`allowBuilds`).
- TypeScript is **v7** (native), which `typescript-eslint` doesn't support. Lint with **oxlint** (`web/.oxlintrc.json`), not ESLint.
- A manual server uses a random port and a scratch `WISHBONE_DATA_DIR` under `./tmp/`, so it doesn't touch your dev server or data.
- The OpenAPI document at `/api/v2/openapi` is the contract. A test compares it with `internal/server/testdata/openapi.json`, so commit the refreshed copy. Return errors with `server.Error(status, code, detail)`, using the codes in `docs/routes.md`.
- Never edit the baseline migration (`internal/store/migrations/20261110030443_baseline.sql`). A schema change is a new migration. If it changes a table or column that holds an image key, it also adds an entry to `schemas` in `internal/imagerefs`, so a snapshot can read the older database.
- SQLite: no `STRICT` tables with time columns. They reject `TIMESTAMP`, which the driver needs to scan `time.Time`.
- Commits and PR titles follow Conventional Commits with a lowercase subject (lefthook runs commitlint). A message must not contain a `Claude-Session:` line or a session URL: commitlint rejects them. `Co-Authored-By` is fine.
- Never commit `.agent/`, `.claude/`, or `*.local.md`. Private scratch goes in the gitignored `.agent/`.

## Process rules

- Branch: `<type>/<N>-<slug>`, for example `fix/123-hide-claim`, where `N` is the issue number. **Never push to `main`.** Work on a branch and open a PR.
- **A PR merges** with `gh pr merge --squash` only when someone who wrote none of it approved its head commit in review, and `PR Title` and `CI OK` are green. Never turn on auto-merge for a PR: it is on in the repo only for Renovate. See [Merge](docs/workflow.md#merge).
- **Issues are for humans.** Don't put agent working notes, logs, or decisions in issue bodies or comments (see [Notes and memory](docs/workflow.md#notes-and-memory)). Issues use the labels in [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).
- [`docs/agents/`](docs/agents/domain.md) configures the maintainer's own agent skills (the mattpocock/skills pack). Ignore it if your agent doesn't use that pack.
