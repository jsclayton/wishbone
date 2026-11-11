---
status: accepted
---

# Native apps redeem a login link for a bearer session

A native app signs in by redeeming a login link, from its QR code or a paste, with `"session": "bearer"`. The link holds both the instance's address and the token, so one build of the app works with every instance and needs no domain association. The token is an ordinary session: it expires 365 days after its last use, slides forward on each request, and has no refresh token. The web app keeps the `HttpOnly` cookie. [ADR 0001](0001-login-links-and-passkeys-instead-of-oidc.md) still holds: there is no OIDC. No native app is part of this repo: a native app is a separate project, and this decision is what the API offers it.

Wishbone is self-hosted, so the app can't know the instance's domain when it is built. Most users open Wishbone a few times a year, around birthdays and the holidays, and only ever sign in with a link someone sends them. A sign-in that expires, or that needs setup on each instance, would cost them more than a long-lived token risks.

## Considered Options

- **Short access tokens with refresh tokens.** Rejected. RFC 6750 recommends short-lived tokens, but a public client's refresh tokens must rotate or be bound to the client (RFC 9700). Rotation adds a token pair, a refresh route, and reuse detection, and binding adds mTLS or DPoP. A failed refresh signs out a user who visits twice a year. The server already slides a session's expiry on each use, and a session can be revoked from the sessions list.
- **A bearer token in the web app too, with no cookies.** Rejected. Safari deletes storage that scripts write after 7 days of use with no visit to the site, so most users would be signed out at each visit. An `<img>` can't send an `Authorization` header, so every image would need `fetch` and a blob URL, or signed URLs. A script injected into the page could read the token and use it elsewhere for a year.
- **Sign-in in a system browser sheet, then a code exchanged for a token with PKCE.** Deferred. It works with any domain, and it would give a passkey sign-in in the app. But it needs an authorize page, a code-for-token route, and PKCE checks. Whether passkeys work inside the iOS sheet is unconfirmed, and only a built app can test it. It can come later as an additive change.
- **Native passkeys, and links that open in the app.** Deferred. On iOS, every instance's domain must be in the app's entitlement when it is built, and each instance must serve files that name the app. So one store build can't serve every instance. It can come later, for example for an instance that builds its own app.

## Consequences

- `POST /login-links/redeem` takes an optional `session` field. With `bearer`, the token is in the body, and there is no cookie.
- Only login link redemption gives a bearer token. A user who is signed in on the web and has no link makes one for themselves on their sign-in screen, then scans or pastes it into the app.
- A stolen token works until someone revokes it or it goes unused for 365 days. The app keeps it in the platform's secure storage, out of backups.
- The login link page has no "Open in app" button, because a custom URL scheme can deliver the token to another app.
