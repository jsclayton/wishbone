# Security

## Report a vulnerability

Report it in private. Don't open a public issue or pull request for a vulnerability.

1. Open the **Security** tab of this repository.
2. Choose **Report a vulnerability**. This is GitHub's private vulnerability reporting.
3. Say what you found, the steps to reproduce it, and what an attacker gains. A short proof of concept helps.

The maintainer reads the report and replies in the same private thread. Wishbone is a project for one family's instance, kept by one person, so a fix can take some days.

## What is in scope

Above all, any way that **a list's recipient can learn claim state**. A recipient must never learn the claim state of their list: whether an item is claimed, who claimed it, or what a giver recorded about it (see [Who sees a gift](docs/spec.md#who-sees-a-gift)). This covers a direct leak in a response, and a side channel such as:

- status codes, error codes, or response times that differ with claim state
- ETags or versions that change when a claim changes
- counts, for example `item_count`, that include claimed or hidden items
- images, avatars, or any other file that a claim changes or reveals

Also in scope:

- sign-in and session flaws: login links, passkeys, session cookies and tokens, and the setup code
- a user who can see or change another user's data without the right role, group, or parent link
- server-side request forgery (SSRF) through link reading and image download, for example a way to make the server reach a private address, or to read a local file
- injection, cross-site scripting, and request forgery in the web app and the API

## What is out of scope

- A problem that needs access to the server host, its data directory, or the database file. Anyone with that access can read every claim.
- Missing hardening headers or rate limits with no attack that uses them.
- A report from an automated scanner with no demonstrated effect.
- A weak setup of your own instance, for example plain HTTP without a reverse proxy.

## Supported versions

Only the newest release gets security fixes. Upgrade to it before you report, and say which version you tested.
