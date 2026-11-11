# Contributing

Thanks for wanting to help. Wishbone is kept by one person, for one family's instance, so it stays small on purpose. Please read this before you start.

Everyone who takes part in this project follows the [Code of Conduct](CODE_OF_CONDUCT.md).

## Before you write code

Read these three files first:

- [`docs/spec.md`](docs/spec.md): what Wishbone does. It is the source of truth. If the code and the spec differ, one of them is wrong.
- [`GLOSSARY.md`](GLOSSARY.md): the terms. Use them in code, UI copy, and docs.
- [`docs/development.md`](docs/development.md): how to set up, build, and run it, and how the repo is laid out. `just --list` shows every recipe.

[`docs/architecture.md`](docs/architecture.md) says where each rule lives in the code. [`docs/rationale.md`](docs/rationale.md) and [`docs/adr/`](docs/adr/README.md) say why the big choices were made. Read the matching entry before you argue with one.

## Open an issue first

For a feature or a change of behavior, open an issue before you write code. Say the problem, not only the fix. The spec decides what Wishbone does, and the maintainer decides spec questions. A pull request that changes behavior the spec doesn't describe will wait until the maintainer answers.

You can send a pull request without an issue for a typo, a small bug fix, or a missing test. A security problem is not an issue: see [`SECURITY.md`](SECURITY.md).

## How to work

- Use **pnpm** for JavaScript, never npm.
- Work test-first: write a failing test at the API or in the browser that shows the behavior, then make it pass. Don't test private details.
- Before you push, run these and keep them green:
  - `just lint`
  - `just test`
  - `just test-e2e`, when user-facing behavior changes
- A route change changes the OpenAPI document. Run `go test ./internal/server -run OpenAPIDocument -update` and commit the copy (see [Tests](docs/development.md#tests)).
- Change the spec and the glossary in the same pull request as the behavior, but only after the maintainer has agreed to the change.
- Follow [Conventional Commits](https://www.conventionalcommits.org/) for each commit and for the pull request title, for example `fix: keep the claim hidden from the recipient`. `just install` sets up lefthook, which runs commitlint on each commit. CI checks the title.
- Keep a pull request narrow. If it grows, split it.
- Don't commit private notes or scratch files (`.agent/`, `.claude/`, `*.local.md`).

## License

Wishbone is under the [AGPL-3.0](LICENSE). When you contribute, you agree that your contribution is under the same license: what comes in is what goes out. There is no contributor agreement to sign.

## The full process

[`docs/workflow.md`](docs/workflow.md) describes the whole process: issues, branches, review, merge, and CI. It is written for the maintainer and the agents they run, so some of it doesn't apply to a person. What you do differently:

- **You don't post a verdict comment.** The `Verdict: approve` comment is how an agent reviewer reports, because agents can't approve their own work on GitHub. The maintainer reviews your pull request and merges it.
- **You don't merge your own pull request.** The maintainer squash-merges it when `PR Title` and `CI OK` pass.
- **You don't need the triage labels.** The maintainer triages issues and sets `ready-for-agent` and the other labels.
- **You write the pull request body in your own words.** Use the template: say what changed and why, the choices you made, and any issue you opened. Leave the review summary blank. The maintainer fills it in.
- **You don't have to file issues for what you find.** If you notice a problem outside your change, mention it in the pull request or open an issue.

Questions are welcome in [Discussions](https://github.com/jsclayton/wishbone/discussions).
