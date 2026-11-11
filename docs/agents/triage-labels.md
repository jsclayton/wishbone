# Triage labels

These files in `docs/agents/` configure the maintainer's own agent skills (the [mattpocock/skills](https://github.com/mattpocock/skills) pack). They are not part of Wishbone. Ignore them if your agent doesn't use that pack.

Issues use these labels. [`docs/workflow.md`](../workflow.md#tickets) says how a ticket moves through them.

| Label | Meaning |
|---|---|
| `needs-triage` | The maintainer needs to evaluate this issue. Every new issue starts here. |
| `needs-info` | Waiting on the maintainer for a product or spec decision. |
| `ready-for-agent` | Fully specified, and an agent can take it. |
| `ready-for-human` | A person must do the work. |
| `wontfix` | Will not be done. |

Renovate also uses `dependencies` and `major-update` (see [`docs/workflow.md`](../workflow.md#merge)).
