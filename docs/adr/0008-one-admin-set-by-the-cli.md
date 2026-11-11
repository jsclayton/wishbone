---
status: accepted
---

# One admin, set by the CLI

Exactly one **admin** has powers over every user. Only the CLI sets the admin. The first user is the admin, and `wishbone set-admin` moves the role. Admin is an explicit flag on the user that doesn't depend on how the user signs in. We considered granting management powers to anyone who can sign in without a login link (a passkey or OIDC), and rejected it. With that rule, a kid who adds a passkey becomes able to manage users, and a trusted grandparent who never sets up a passkey never can. A passkey is only a convenience for recovery. A stolen session cookie is equally dangerous with or without a passkey, so the admin has no credential prerequisites.

Nothing in the API grants the role. So a stolen session can't make another admin, and the role stays with the operator, who can read the logs and run the CLI. Setup uses the same trust: the first user needs the setup code from the log.

## Considered Options

- **Several admins who are peers, granted in the CLI.** Rejected for now. A family instance has one person who runs it, and one admin is simpler to explain. To allow several admins later is an additive change: the flag is already there, and only the rule about how many changes.
- **Admins who each have powers over only some users.** Rejected. Nothing links an admin to some users and not others, so there is no rule for which users an admin can act on.
- **Grant the admin flag in the app.** Rejected. The maintainer sees no need for it, and it would let one session give the role to others.

## Consequences

- Nobody can deactivate the admin, and `delete-user` refuses the admin. To remove the user who is the admin, the operator first runs `set-admin`. So the instance always has an active admin, and setup can't run again.
- If the admin is away, nobody in the app can change a group, and only parents can recover a sign-in, for their own children. `wishbone login-link` still works for every user.
- The admin manages every group and shared occasion, so there are no group owners, and no shared occasion is left without someone who can edit it.
