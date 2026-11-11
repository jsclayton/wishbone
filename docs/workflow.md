# Workflow

How work moves from the spec to merged code. These rules apply to everyone who changes the repo, a person or an agent. How the maintainer runs their own agents is not part of the repo. An outside contributor reads [`CONTRIBUTING.md`](../CONTRIBUTING.md) first.

## Overview

```
spec.md ──► tickets (GitHub issues, with blockers) ──► branch and PR ──► review ──► CI ──► squash merge
```

The maintainer is the person who owns the repo and decides what Wishbone does. They approve the ticket breakdown, decide product and spec questions, and can revert any merge.

## Tickets

A ticket is a GitHub issue that describes one piece of work.

- Break `docs/spec.md` into **tracer-bullet tickets** (each one a thin slice through every layer): schema, API, UI, and tests. Each one can be demoed on its own and fits in one working session.
- Each ticket lists the tickets that **block** it and cites the spec sections it implements.
- Size a ticket as one slice that can be demoed, with its tests.
- The maintainer approves the breakdown before any ticket is published as an issue.
- New issues get `needs-triage`. The label meanings are in [`docs/agents/triage-labels.md`](agents/triage-labels.md). A ticket is ready to work on when it has `ready-for-agent` and all its blockers are merged.
- `needs-info` means a product or spec question is open and waits on the maintainer. A ticket with an open question is not `ready-for-agent`. `ready-for-human` is like `ready-for-agent`, for work a person must do. `wontfix` means it won't be done.

## Branches and commits

- Branch: `<type>/<N>-<slug>`, where `<type>` is the Conventional Commit type of the change, for example `feat`, `fix`, or `docs`.
- Commits and PR titles follow Conventional Commits.
- Commits carry no session link: no `Claude-Session:` line and no `claude.ai/code/session` URL. A `Co-Authored-By: Claude` line is fine. The `commit-msg` hook rejects the session link, so remove it from the message before you commit. Pushed history is not rewritten, so tell the maintainer if a session link gets pushed.
- Work test-first at the seams the ticket names. `just lint` and `just test` stay green. Run `just test-e2e` when user-facing behavior changes.
- If the spec is ambiguous or seems wrong, ask. Spec changes go through the maintainer. Glossary changes are allowed when a new term is resolved: call them out in the PR.

## Review and the PR

- Every change is reviewed on two axes against the default branch: repo standards, and the ticket and spec. The reviewer wrote none of the change.
- Before you push, rebase on `origin/main` and run `just lint` and `just test` locally, and `just test-e2e` if user-facing behavior changed.
- Open a **ready** PR. The title is a Conventional Commit (CI checks it). The body says `Closes #<issue>` and follows [the PR body](#the-pr-body).
- Local checks catch most failures, so CI runs about once per PR. CI must be green.

### The PR body

The PR body is where a reader learns what changed and why. Write it in plain language, and keep it short. Use the template:

- **Summary:** what the change does, and why.
- **Decisions and notes:** each judgment call, with the reason. Include where it resolved an ambiguity in the ticket or spec, and any answer the maintainer gave to a question. A reader can disagree with any of them and revert.
- **Testing:** what you ran.

Scratch files are never linked or copied raw. Keep the PR body current until the merge.

### Issues you find

You can file a GitHub issue for a problem that is out of the ticket's scope: a bug in code you didn't change, a spec gap, a missing test, or a deferred review finding. This gives the maintainer a place to triage without growing the PR.

- First search the open issues, and don't file a duplicate.
- Each issue has a clear title, a short body (the problem, where it shows, why it matters, and what done looks like), and the `needs-triage` label. Nothing else: no `ready-for-agent`, no blockers, no parents.
- Issues are for people. An issue is never a working note, a log, or a to-do.
- Link each issue you file from the PR body.
- A finding that blocks the ticket is not an issue. Raise it as a question.

## Merge

- Merge with `gh pr merge --squash`, when all of these are true:
  - The PR is ready for review, not a draft.
  - Someone who wrote none of the change approved it in review, for the current head commit. A push after the review needs a new review.
  - `PR Title` and `CI OK` pass.
  - No spec question in the PR is still open.
- A contributor doesn't merge their own PR. The maintainer merges it.
- Never turn on auto-merge for a PR. The setting is on only for Renovate.
- Exception, Renovate: Renovate merges its own minor and patch updates when CI is green, with no review. It uses GitHub's auto-merge. A major update (Renovate labels it `major-update`) is never merged this way: it gets a review like any other PR.
- If the maintainer disagrees with a merge, they revert it with a PR.
- The squash commit uses the PR title and body. See [Repo settings](#repo-settings).
- The `Commitlint` workflow lints the subject of each commit pushed to `main`. It can't block the merge. If someone edits the title in the merge dialog and breaks the rules, `main` turns red.

### Repo settings

These GitHub settings enforce the merge rules:

- **Merge button:** only squash merge is allowed. The squash commit uses the PR title and the PR body.
- **Auto-merge:** allowed.
- **Branches:** GitHub deletes the head branch after the merge.
- **Ruleset `main`:** applies to the default branch, with no bypass actors. It has two rules:
  - **Pull request:** every change to `main` goes through a PR. Squash is the only merge method. No approving review is required, because GitHub doesn't let an author approve their own PR, and the maintainer's PRs are reviewed outside GitHub. The review is a rule, not a setting.
  - **Required status checks:** `PR Title` and `CI OK` must pass before a merge. `CI OK` fails if any CI job it depends on fails or is cancelled. A skipped job counts as passed. Only GitHub Actions can satisfy these checks. The branch does not have to be up to date with `main` before a merge, so parallel PRs don't need a rebase for each merge.
- **`review` status:** a commit status named `review` records that a review approved that commit. It is not a required check, so Renovate can still merge.

### CI cost

Every job runs on a GitHub-hosted runner, which is free for a public repo. CI still avoids needless runs:

- CI doesn't run on draft PRs.
- Editing a PR title or body runs only the `PR Title` workflow.
- A change to docs only skips the Go, Web, and E2E jobs. Docs are `docs/`, `LICENSE`, any `*.md` file, `.github/ISSUE_TEMPLATE/`, and `.github/pull_request_template.md`.
- A PR runs `Image Check`, which builds an amd64 image, only when one of these files changes: `Dockerfile`, `.dockerignore`, `go.mod`, `go.sum`, `package.json`, `web/package.json`, `pnpm-lock.yaml`, `pnpm-workspace.yaml`, or `.github/workflows/ci.yml`. A change to source code alone doesn't run it. It never pushes the image.
- These two lists copy `docs_only` and `image_inputs` in the `Changes` job of `ci.yml`. If they differ, `ci.yml` is right.
- The multi-arch image is built and pushed to GHCR from `main` only. It gets the tags `main`, `unstable`, and `sha-<short>`.
- On `main`, all pushes share one concurrency group, and the run in progress always finishes. GitHub keeps only the newest waiting run and cancels the older waiting ones. So a burst of merges runs at most two runs: the one in progress and the newest. A merge whose run is cancelled gets no `sha-` image of its own. The newest run builds its code.
- On `main`, `Changes` compares with the commit of the current `main` image, not with the previous push. So the newest run tests and builds every code change since the last image, which includes the code of cancelled runs. A docs-only merge after a built code merge skips `Go`, `Web`, `E2E`, and `Image`. If `Changes` can't read that commit from GHCR, it runs everything. It reads the image with `packages: read`.
- `Image` moves `main` and `unstable` only forward. It moves them only if the current `main` image was built from the run's commit or from an ancestor of it. If it can't read the `main` image, or the image's commit isn't on `main`, it moves them only from the tip of `main`, so a lost tag comes back. A re-run of a run for an older commit pushes only its `sha-` tag. A re-run on `main` also waits in the same group, so it can replace the newest waiting run. A re-run does not rebuild an image that is already current, because `Changes` then skips `Image`. If the newest run's `Image` failed, re-run it. A missing `main` tag comes back with the next run that builds an image.
- `Image`, `Image Check`, and `release` use no build cache, so a build never reads a poisoned cache entry. The cache saved little, because the two Go compiles run on every build. The other jobs keep the Go, pnpm, and mise caches.
- The E2E tests run in the `E2E` job, one job per Playwright project (`desktop-chromium` and `mobile-safari`), at the same time on separate runners. Each job builds the frontend and the e2e binary itself and starts its own server with a fresh data dir. A shared build artifact would need one more job in front of both, which takes longer than the ~40 s build. Lint, typecheck, and unit tests run once, in `Web`. Each project runs every spec, so a new spec needs no change to CI. `CI OK` fails if either `E2E` job fails or is cancelled. A failed job uploads its own `playwright-results-<project>` artifact.

Every job runs on `ubuntu-26.04`. Don't use a self-hosted runner: in a public repo it would run code from anyone's fork PR. The `Web` and `E2E` jobs run in the Playwright container image, which has the browsers and their system packages. On a pull request from a fork, every token is read-only whatever `permissions:` says, and no job gets secrets. Only `Image` and `release` have write-scoped tokens, and they run only on `main` and for releases.

## Notes and memory

The repo is public. It holds only what a human contributor needs. Issues and PRs are for people. Put each kind of information in one place:

| Kind | Where |
|---|---|
| Lasting decisions and why | `docs/adr/` and `docs/rationale.md`, in the PR that makes the decision |
| Behavior and terms | `docs/spec.md` and `GLOSSARY.md` (spec changes go through the maintainer) |
| How and why a change was made | The PR description ("Decisions and notes") and review comments |
| Rules for every session | `AGENTS.md`, kept short |
| Scratch: progress, dead ends, next steps | `.agent/`. It is gitignored and never published |
| Memory across sessions or tickets | Outside the repo, or a private notes repo linked in as `.agent/` |

Rules:

- Issue bodies and comments hold the problem, acceptance criteria, and human discussion. Agents don't log working notes there.
- Before you open a PR, move anything lasting from `.agent/` into an ADR, a rationale entry, or the PR body. Leave the rest to expire.
- Nothing from `.agent/`, `.claude/`, or `*.local.md` is committed. A pre-commit hook and CI both check this.
- No direct pushes to `main`. The ruleset blocks them (see [Repo settings](#repo-settings)).

## Rules for parallel work

- **Conflict hotspots:** these files change in many tickets. Merge in dependency order, and have the later PRs rebase.
  - Go: `internal/server/server.go` (it registers each resource's routes), `cmd/wishbone/main.go` (it builds the services), and `internal/server/testdata/openapi.json`. Never merge the OpenAPI copy by hand. After a rebase, run `go test ./internal/server -run OpenAPIDocument -update` and check that the diff holds only your routes.
  - Web: `web/src/routes.tsx` (one line for each screen), `web/src/api.ts` (one function for each call), and `web/src/Menu.tsx` (one row for each screen).
  - E2E: `web/e2e/support.ts`.
- **Migrations:** create them with `just migration <name>`. It uses a timestamp version with the real creation time, so parallel branches don't collide. The baseline is never edited (see [The baseline migration](development.md#the-baseline-migration)).
- **Ports:** every branch in progress uses its own `WISHBONE_PORT`. A manual server uses a scratch `WISHBONE_DATA_DIR` under `./tmp/`. For Playwright, set only `WISHBONE_PORT`. It creates a fresh data dir for each run.
- **Shared E2E state:** all E2E specs share one server and one database. In CI, each Playwright project runs against its own server. `web/e2e/global-setup.ts` sets up the shared server once, before any spec: it reads the setup code from the server log and makes the admin (Ada Lovelace). A spec that calls `test.use({ signedIn: true })` starts signed in as the admin, in the shared session. The shared session must not end or change, so a spec that logs out, or needs an instance with no users, starts a server of its own with `startServer` (`web/e2e/server.ts`). Parallel tickets that add specs must not depend on state another spec changes.
- **Spec changes go through the maintainer.** Raise them as a question.
- **Keep PRs narrow.** If a ticket grows, split it rather than widen the PR.
