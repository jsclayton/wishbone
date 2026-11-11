---
status: accepted
---

# Login links and passkeys instead of OIDC

Almost every user will only ever sign in through a link or QR code that a parent or the admin sends them, so Wishbone has no OIDC. The first user is made with a setup code that the server writes to its log while the instance has no users (see [Setup](../spec.md#setup) in the spec), and that user is the admin ([ADR 0008](0008-one-admin-set-by-the-cli.md)). `wishbone login-link` is the permanent break-glass path. Passkeys cover recovery and signing in from a signed-out browser. OIDC can be added later as an extra sign-in method a user links to their profile. It is never a separate kind of account.

A parent makes a child's login link ([ADR 0006](0006-parents-see-and-edit-their-childrens-lists.md)). A native app redeems a login link for a bearer session ([ADR 0013](0013-native-apps-redeem-a-login-link-for-a-bearer-session.md)).
