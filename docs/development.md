# Development

## Prerequisites

- [mise](https://mise.jdx.dev), activated in your shell
- Docker, only for `just docker-build`

`mise.toml` pins Go, Node, pnpm, and just. CI uses the same Node and pnpm pins and reads Go from `go.mod`.

## First run

```bash
mise trust
mise install
cp .env.example .env
just install             # pnpm install; also installs git hooks (lefthook + commitlint)
just install-playwright  # once, for e2e tests
```

## Daily loop

```bash
just dev-backend    # Go server on :8080, data in ./tmp/dev-data
just dev-frontend   # Vite on :3000 with HMR, proxies /api to :8080
```

Open http://localhost:3000. A new data dir has no users. The server writes a one-time setup code to its log (`setup_code=...`), and the web app shows a setup screen where you enter it and the first user's name and birthday. That user is the admin. Each start with no users makes a new code. The API is at `/api/v2` (`/api/v2/docs` is its reference). A request to an API version below the lowest one that the server runs answers 410 (see [Versions of the API](spec.md#versions-of-the-api)).

## Recipes

`just --list` shows every recipe. The main ones:

| Recipe | What it does |
|---|---|
| `just build` | Frontend + backend into `bin/wishbone` with the frontend embedded |
| `just test` | Go tests (race + coverage) and Vitest. The Go run is slow (see [Tests](#tests)) |
| `just build-e2e` | Like `just build`, with the `e2e` tag (see [The e2e build](#the-e2e-build)). Never ship it |
| `just test-e2e` | Runs `build-e2e`, then Playwright against `bin/wishbone` |
| `just icons` | Regenerates the PNG icons in `web/public` from `favicon.svg` with `web/scripts/make-icons.mjs` (Playwright's Chromium). Run it after you change the favicon, and commit the PNGs |
| `just lint` | golangci-lint, oxlint (warnings fail), actionlint and shellcheck (workflows and `.github/scripts`), and `tsc` |
| `just fmt` | gofmt + goimports via golangci-lint |
| `just migration <name>` | Creates a timestamped SQL migration in `internal/store/migrations` (see [Migrations](#migrations)) |
| `just docker-build` | Builds the release image |

## Repo layout

```
cmd/wishbone/       The CLI (serve, users, login-link, set-admin, delete-user, backup,
                    healthcheck, version). It reads the config and wires the services together.
internal/
  server/           The HTTP router, the handlers, the OpenAPI document, and the embedded web app
  user/ list/ item/ gift/ occasion/ group/ image/
                    One package for each kind of resource: its types, its rules, and its SQL.
                    They import no HTTP package.
  giftflag/         The flags on a giver's open gift
  loginlink/ passkey/ session/ setup/
                    Sign-in: login links, passkeys, sessions, and the first user
  userdelete/       Permanent deletion of a user, for `wishbone delete-user`
  extract/          Reads a product page: a direct fetch, and Bright Data Web Unlocker
    pipeline/       Runs the stages in order, with the 24-hour cache and the hourly limit
    llm/            Fills in what the page left out, and reads free text and photos
  netguard/         Keeps fetches of a user's URL off the host's own network
  blocklist/        The instance's blocked stores
  store/            The SQLite database and its migrations (`store/migrations`)
  imagestore/       Image files on disk
  imagerefs/        Which image keys a database still uses, for backups
  backup/           Snapshots of the database and the images
  config/ logging/ telemetry/ clientip/ ratelimit/
                    Configuration, logs, optional tracing, the client IP behind a proxy,
                    and an in-memory rate limiter
  page/ patch/ money/ initials/
                    Paging and sort rules, JSON Merge Patch fields, prices in minor units,
                    and avatar initials
web/
  src/              The React app. A test sits next to its source, as `X.test.ts(x)`.
  e2e/              Playwright specs, and `support.ts` and `server.ts` for them
  public/ sw/       Icons and the manifest, and the template of the service worker
  scripts/          `make-icons.mjs`, for `just icons`
docs/               The spec, the route table, the ADRs, and the guides
```

[`architecture.md`](architecture.md) says where each rule lives.

## How the pieces fit

- `web/` is the React app. `pnpm --filter web build` writes `web/dist/`.
- The Go binary embeds `internal/server/web/dist/`. `just build-backend` copies `web/dist/` there. Recipes that only need Go to compile write a placeholder `index.html` instead.
- `internal/server/testdata/openapi.json` is a copy of the OpenAPI document. A Go test compares it with the server's document, so a change to the contract shows in the PR diff. `go test ./internal/server -run OpenAPIDocument -update` rewrites it.
- `internal/store/migrations/` holds goose SQL migrations, embedded and applied on `serve` startup after an [automatic snapshot](operations.md#automatic-snapshots). The first one is the baseline, and it is never edited. A schema change is a new file made with `just migration <name>`. See [Migrations](#migrations).
- Go modules are vendored and committed (`vendor/` is about 150 MB, so a clone is large). After changing dependencies, run `go mod tidy && go mod vendor` and commit `vendor/`. Renovate does this automatically.
- `pnpm-lock.yaml` pins JavaScript dependencies. `just install` uses `--frozen-lockfile`.
- The Docker build images pin Node and Go patch versions and multi-arch digests. Renovate groups each runtime's updates across mise, manifests, and Docker.
- Renovate also groups pnpm updates across `mise.toml`, `package.json`, and the Dockerfile.
- The service worker exists in release builds only. `shellWorker` in `web/vite.config.ts` fills `web/sw/template.js` and writes `dist/sw.js`. `just dev-frontend` doesn't register it.
- To remove the worker later, ship a `sw.js` that unregisters itself. Don't just delete the file: the server would answer `/sw.js` with `index.html`, the browser would reject that, and clients would keep the old worker for good.

## Tests

Write a test that fails for the behavior you want, then make it pass. Test at the API (Go, in `internal/server`) or in the browser (Playwright), and don't test private details. A package's own tests sit next to it as `*_test.go`.

| To run | Command |
|---|---|
| Everything, with `-race` and coverage | `just test` |
| The Go tests only | `just test-backend` |
| One Go package | `go test ./internal/gift` |
| One Go test | `go test ./internal/server -run TestName` |
| The OpenAPI copy, after a route change | `go test ./internal/server -run OpenAPIDocument -update` |
| The Vitest tests only | `just test-frontend` |
| One Vitest file | `pnpm --filter web exec vitest run src/Claim.test.tsx` |
| All the e2e specs | `just test-e2e` |
| One e2e spec | `just build-e2e && pnpm --filter web test:e2e e2e/gifts.spec.ts` |

`internal/server` is the slow package: with `-race` it takes about 7 minutes on a 10-core laptop and about 11 in CI. Go's default test timeout is 10 minutes, so `just test-backend` sets 25. For a quick loop, run one package without `-race`.

Go needs a frontend in `internal/server/web/dist/` to compile. `just` writes a placeholder when there is none.

Playwright starts `bin/wishbone` itself, on `WISHBONE_PORT` (8089 by default) with a fresh data dir. Set a random port so you don't collide with another run:

```bash
WISHBONE_PORT=9234 just test-e2e
```

`just test-e2e` builds the e2e binary first (see [The e2e build](#the-e2e-build)). The first user, the admin, is made for you by `web/e2e/global-setup.ts`, and a spec that calls `test.use({ signedIn: true })` starts signed in as that user. A spec that needs an instance with no users, or that logs out, starts its own server with `startServer` (`web/e2e/server.ts`). Specs share a server, so one spec must not depend on state that another changes. CI runs each Playwright project on its own runner.

## The e2e build

Browser tests must not call real services. The e2e binary (`just build-e2e`, which `just test-e2e` runs; CI builds it with `-tags e2e`) can let link reads and image downloads connect to loopback and private addresses, so a spec points them at a fixture page served from the test process (`web/e2e/search.spec.ts`). The switch is `WISHBONE_EXTRACT_ALLOW_PRIVATE=true` (`cmd/wishbone/allowprivate_e2e.go`) and exists only under the `e2e` build tag. The release build, `just build`, and the Dockerfile compile `allowprivate.go` instead, which always says no, and a test (`allowprivate_test.go`) keeps that true. The e2e binary logs a warning when the switch is on. Never ship it. Only `just build-e2e` passes the tag (as an argument to `build-backend`), so a `GO_BUILD_TAGS` in the environment or in `.env` changes nothing. `just test` and `just lint` also test and lint `./cmd/...` with `-tags e2e`, and CI does the same, so the tagged files are not left unbuilt. The same build has a test endpoint for reveal. With `WISHBONE_E2E_HOOKS=true` (`web/playwright.config.ts` sets it), `POST /api/e2e/reveal` with `{"recipient": <user id>, "advance": "10000h"}` gives the gifts on the lists that user is a recipient of, as if the clock were that far ahead, and answers `{"revealed": n}` (`cmd/wishbone/e2ehooks_e2e.go`). It does not move the server's clock and touches no other user's gifts, so specs that share the server stay apart. The release build compiles `e2ehooks.go` instead, which adds nothing, and a test (`e2ehooks_test.go`) keeps that true. The LLM needs no switch: a spec sets `WISHBONE_LLM_PROVIDER=openai` and `WISHBONE_LLM_BASE_URL` to a fixture endpoint.

## Running a throwaway server

Use a random port and a scratch data dir so you don't collide with a running dev server:

```bash
WISHBONE_PUBLIC_URL=http://localhost:9234 WISHBONE_PORT=9234 \
WISHBONE_DATA_DIR=./tmp/scratch-9234 ./bin/wishbone serve
```

## Migrations

`internal/store/migrations/` holds goose SQL migrations. The server applies them at `serve` startup, after an [automatic snapshot](operations.md#automatic-snapshots).

- Make a migration with `just migration <name>`. Its version is the real creation time, so branches in parallel don't collide. Write only the `Up` part.
- What snapshots and the version guard must keep true is in [`architecture.md`](architecture.md#snapshots-and-the-version-guard).

### The baseline migration

`internal/store/migrations/20261110030443_baseline.sql` creates the whole schema, including `instance_meta` and `extraction_cache`. It has no down.

- Its version is `store.BaselineVersion`. A new install runs the baseline. If a database lists the versions of other migrations as well as the baseline's, goose finds nothing pending for them: it ignores the versions that have no file. A database below the baseline's version is older than this release supports, and the version guard refuses it.
- Never edit the baseline. A schema change is a new migration (`just migration <name>`). If it changes a table or column that holds an image key, it also adds an entry to `schemas` in `internal/imagerefs`.
- A test of code that must read an older schema builds that database with `Store.MigrateTo(ctx, version)`. It needs a migration after the baseline: the baseline is the oldest schema a test can build.

## Releases

Publish a GitHub release tagged `vX.Y.Z`. The release workflow builds a multi-arch image and pushes `ghcr.io/jsclayton/wishbone` with `X.Y.Z`, `X.Y`, `X`, and `latest` tags. `X.Y`, `X`, and `latest` move only if the release is the highest `vX.Y.Z` tag for that line, so a patch for an older line never moves a tag of a newer one. A CI run on `main` that changes code since the current `main` image pushes `unstable`, `sha-<short>`, and `main`. See [CI cost](workflow.md#ci-cost). PRs don't push images.
