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
- **Make an issue a sub-issue**: `gh issue create --parent <parent> ...`, or `gh issue edit <parent> --add-sub-issue <child>` afterwards (`gh` 2.94 and later). Without sub-issues, put `Part of #<parent>` at the top of the child body.
- **Blocking**: use GitHub's issue dependencies. Add an edge with `gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric database id (`gh api repos/<owner>/<repo>/issues/<n> --jq .id`, not the `#number`). A ticket is unblocked when every blocker is closed.

GitHub shares one number space between issues and PRs. To read a bare number, try `gh pr view <n>`, then `gh issue view <n>`.

Issues are for people: see [Notes and memory](../workflow.md#notes-and-memory).
