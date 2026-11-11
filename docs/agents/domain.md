# Domain docs

These files in `docs/agents/` configure the maintainer's own agent skills (the [mattpocock/skills](https://github.com/mattpocock/skills) pack). They are not part of Wishbone. Ignore them if your agent doesn't use that pack.

This repo has one context. Before you work in an area:

- Read [`GLOSSARY.md`](../../GLOSSARY.md) and use its terms in code, tests, UI copy, and docs. If a term you need is missing, say so in the PR.
- Read the ADRs in [`docs/adr/`](../adr/README.md) that touch your area. An ADR's `status` says whether it holds.
- If your change contradicts an ADR, say so in the PR. Don't override it silently.
