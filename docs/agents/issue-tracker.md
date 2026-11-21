# Issue tracker: GitHub

These files in `docs/agents/` configure the maintainer's own agent skills (the [mattpocock/skills](https://github.com/mattpocock/skills) pack). They are not part of Wishbone. Ignore them if your agent doesn't use that pack.

Issues (tickets) live in GitHub Issues. The spec is [`docs/spec.md`](../spec.md). Use the `gh` CLI for all operations. `gh` infers the repo from `git remote -v`.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for a multi-line body.
- **Read an issue**: `gh issue view <number> --json number,title,body,labels,comments`.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments`, with `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`.
- **Apply or remove a label**: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`. The labels are in [`triage-labels.md`](triage-labels.md).
- **Close**: `gh issue close <number> --comment "..."`.
- **Parents**: issues carry no parents. Don't make an issue a sub-issue, and don't write `Part of #<parent>` in a body.
- **Blocking**: issues carry no blockers. An issue is opened when its work starts, so nothing that blocks it is still open. Don't add GitHub issue dependencies.

GitHub shares one number space between issues and PRs. To read a bare number, try `gh pr view <n>`, then `gh issue view <n>`.

Issues are for people: see [Notes and memory](../workflow.md#notes-and-memory).
