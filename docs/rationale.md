# Rationale

Why decisions in [`spec.md`](spec.md) and the scaffold went the way they did, where the reason isn't obvious from the outcome. Hard-to-reverse architectural decisions are in [`adr/`](adr/). This file holds the rest.

If you revisit a decision, read its entry first.

## Product

### UI: "simple, lightweight, and fast"

The design brief is deliberately not "warm", "friendly", or "delightful". Those words reliably produce the generic look of LLM-generated UIs: gradients, oversized rounded cards, emoji headings, soft pastel palettes, marketing-style hero sections. People recognise that look straight away. Build plain, fast, information-first screens: Atkinson Hyperlegible Next for text and Atkinson Hyperlegible Mono for dates and prices, served by Wishbone itself, restrained color, real content (product images, names, dates) as the visual interest. When in doubt, remove styling.

The design is called Timetable and uses the Steel palette (the tokens are in `web/src/index.css`). It uses these fonts, not system fonts, so text looks the same on every device. The maintainer chose them.

### No tab bars

The maintainer rejected them. Menu and the browser's Back are enough to move between screens, and a tab bar takes up room on a 320px-wide phone. Every screen has Menu and big buttons instead.

### Sessions: one person per browser

Account switching on a shared device was considered and rejected. It adds a lot of state and UI for little benefit, because in a typical family everyone has their own device. To sign in as someone else (e.g. a parent as a child), you use a login link, which replaces the current session. A parent who only edits a child's lists doesn't need to: see "Parents edit their children's lists" below.

### Parents edit their children's lists

Children often have a tablet of their own, so a parent could sign in on the child's device. That is simpler than building "edit on behalf of", which needs a mixed recipient/giver view.

Wishbone builds it anyway, because a user can have no sessions at all (a toddler whose parent edits their lists), and because it takes friction away from parents who help with their kids' lists. A parent sees and edits all of a child's lists directly, and the screens show both the edit controls and the claim state when the parent is a giver and not a recipient. See [ADR 0006](adr/0006-parents-see-and-edit-their-childrens-lists.md).

### Groups from day one, requests later

A typical extended family is two branches that should not see each other but both see one shared household. That needs nested groups from the start. The approval flow for adding someone else's members or nesting groups you don't own was deferred: the admin manages every group and every user, and nobody else manages groups.

### Adding items: paste box and photo

- iOS Safari doesn't support the Web Share Target API, so wishbone can't appear in the iOS share sheet as a web app.
- An iOS Shortcut could fill the gap. But a home-screen web app and Safari keep **separate cookie jars**, so a Shortcut that opens Safari would find the user signed out. A token-based Shortcut avoids that but needs per-person setup, which is too much for grandparents.
- Most people in a family use iPhones. An Android share target for the few who don't wasn't worth it.
- A paste box works for everyone with no setup. A token-based Shortcut and an Android share target are future work.
- Family members tend to say "I want that" when they see something. A photo with a temporary title lets them capture it first and tidy it up later.

### Search: direct fetch → Bright Data Web Unlocker → LLM

- Most shops expose JSON-LD `Product` or OpenGraph data, so a direct fetch is free and often enough.
- Big retailers (Amazon, Target, Walmart, Best Buy) block plain fetches. The maintainer already has a Bright Data account. Of its products:
  - **Web Unlocker**: plain HTTP in, rendered HTML out, blocks handled. Fits the existing parse step. **Chosen.**
  - **Browser API**: remote Chromium over CDP. More power than needed, and it needs a browser-automation client.
  - **Web Scraper API**: structured product JSON for big shops, but it's asynchronous and priced differently. Not used to read links.
- A self-hosted headless browser sidecar was dropped: Bright Data handles bot walls better and nothing extra runs on the host.
- Some shops that block a browser user agent serve the real page to a link-preview one (the kind iMessage sends). So the direct stage retries once with that user agent after a blocked or no-data page, before Bright Data costs anything. The maintainer accepted spoofing a user agent for this.
- The LLM fills gaps only. It isn't the primary extractor, which keeps costs low and results predictable.

### No store search

Wishbone doesn't search stores. Search turns a link, typed words, or a photo into suggested item details (see [Search](spec.md#search) in the spec). A store search would need an outside source of product data, and each search and each lookup would cost money, so it would stay off until an instance sets it up. These findings are for a later version:

- Users asked for a way to find a product without leaving the app. Bright Data's account and key were already in place, so a search adds one zone and no new vendor.
- The SERP API gives Google Shopping results as parsed JSON. Those results name a product, but a result's link is a Google page, not a store. A Google page must never become a store link, so the SERP API alone can't finish the job.
- A dataset reads that Google page and names the sellers. Running it for every result would pay for ten lookups to add one item, so it should be the first step of a tap, for the one result the user chose.
- The second step of a tap should read the store's link like a pasted one. A result then gets the same extraction, the same protection, and the same checks as a pasted link, and Web Unlocker still gets its turn when a store blocks a plain fetch.

### LLM providers: `anthropic` + `openai`-compatible

The maintainer has Anthropic keys, but wants to be able to use OpenRouter or a local model. One native Anthropic client plus one OpenAI Chat Completions client with a configurable base URL covers all of them.

### No email, no notifications

This is a private family instance, so email verification protects nothing, and collecting addresses adds data to look after. Login links are sent by hand (text, iMessage) or shown as a QR code. The home screen shows what's new.

### The admin has no credential prerequisite

See [ADR 0008](adr/0008-one-admin-set-by-the-cli.md). A stolen session cookie is equally dangerous with or without a passkey, so requiring a passkey for the admin would be security theater.

### Passkeys: random user handle, and any session can add one

The WebAuthn user handle is 32 random bytes stored on the user (`users.webauthn_handle`), created when they add their first passkey. Authenticators store it, and sign-in looks the user up by it, so changing the format later would break every existing passkey. It is not the user ID, because the WebAuthn spec recommends an opaque random handle and a sequential ID would tell anyone holding an authenticator how many users exist.

Adding a passkey needs only a session. A login link from a parent or the admin, or a link that the user makes for themself (each lasts 7 days), is enough to add a permanent credential. This fits "parents manage their children's sign-in", and the user sees and removes their passkeys under "Your devices". A parent and the admin can also delete a user's passkey, which lets them remove one on a lost device.

### The session cookie is sent once a day, and a request never clears it

A browser applies answers in the order they arrive, and an answer to an old request can arrive after a sign-in in another tab. If every response sent the cookie, a late answer could put the old token back. If a 401 cleared the cookie, a late answer could remove the new one. Either way the user is signed out, and the login link they used is gone.

So a request with a dead session never clears the cookie. A dead cookie does no harm: every read answers 401 and sign-in ignores it. Logging out and revoking your own session still clear it.

The server sends the cookie again on a request that signs in with it, at most once in 24 hours per session, and `sessions.cookie_sent_at` records when. The cookie can't stop being renewed, because Chrome limits its life to about 400 days. The session itself still extends on every use, so the cookie can expire up to a day before the session. A user would have to stay away for almost a year to notice.

A renewal can still lose this race, about once a day at most. We accept that. The server can't take an answer back, and using a revoked session to mint a new one would undo the revocation.

### Why the app shell is cached

Installed use is the main case: the maintainer tells people to add Wishbone to their Home Screen. A cached app shell makes the installed app start quickly and open when the server can't be reached.

- **No offline editing.** The maintainer decided this: no local store, write queue, or sync. Data always comes from the API, so the worker caches only the fixed file list from install and never calls `cache.put`. API data and login link tokens can't enter the cache, and logging out has nothing to clear.
- **Hand-written, with no `vite-plugin-pwa` or Workbox.** The worker is about 40 lines for one fixed file list. That isn't worth a new dependency tree in a lightweight app.
- **Documents are network first, with a 2-second timeout.** With a cache-first document, the first reload after a deploy would still show the old shell, because the browser finds the new worker only afterwards. `index.html` is about 1 KB, and documents load only on a cold start, so the cost is one small request. A failed deploy or a server restart shows the cached shell, not a gateway error.
- **A takeover alone doesn't mean an update.** A reload after a deploy already has the new shell when the new worker takes over. So the page also checks that the worker's shell names a different script or stylesheet. Without this check, every such reload would offer a refresh it doesn't need.
- **A page with no controller watches for a new worker.** The worker doesn't call `clients.claim()`, so a first launch of an installed app stays uncontrolled and never sees a takeover. It would run old code after a deploy until something reloaded it. So that page listens for `updatefound` and treats a worker as an update once it is activated, with the same check. A first install has no active worker, so it stays silent.

## Name

"wishbone" was checked for collisions:

- **GoWishbone** (gowishbone.com) is a family wishlist app, but it's a personal learning project from Spring 2021. The site was last deployed Dec 2021, and there's no public source or company behind it.
- **hintly** was the leading alternative, but hintly.co is a live wishlist product for couples, which is a closer collision.
- Any short gift-themed name collides with something.

**Rule:** the name was kept. A rename would mean replacing the module path, the image name, and the `WISHBONE_` prefix.

## Engineering

These are the choices about the stack, the tooling, and the license, where the reason isn't obvious.

### No OIDC, and one build tag for tests

Some Go projects use a `dev` build tag for a built-in OIDC provider and a `test` tag for fast Argon2id. Wishbone has neither OIDC ([ADR 0001](adr/0001-login-links-and-passkeys-instead-of-oidc.md)) nor password hashing: login-link tokens are high-entropy random values. So the release build has no tags. The one exception is the `e2e` build tag for Playwright, which adds test-only hooks. It must never ship: see [The e2e build](development.md#the-e2e-build).

### License: AGPL-3.0 for the server

Wishbone is a self-hosted network app, so the server is AGPL-3.0. Anyone who runs a changed copy as a service must offer the complete corresponding source of the changed program to its users. A plain GPL would not ask that of a hosted copy. A permissive license would let someone run a closed fork as a service.

A client that only talks to the server through its API (for example a native app) is a separate work. The AGPL-3.0 does not cover it, and its author picks its license. The name and logo are not under the AGPL-3.0: see [`TRADEMARKS.md`](../TRADEMARKS.md).

The maintainer decided this on 2026-11-10.

### pnpm instead of npm

- **Dependency install scripts are blocked by default.** Exceptions are listed in `pnpm-workspace.yaml` under `allowBuilds`. This is the main reason, after the npm supply-chain worms of 2025.
- **`minimumReleaseAge`** (3 days) means a freshly published version isn't installed right away. Renovate uses the same delay. A security fix waits 1 day in Renovate (`vulnerabilityAlerts`), not the default of no wait, because it auto-merges and Go modules and GitHub Actions have no other age check.
- **Strict `node_modules`**: code can only import packages it declares.
- **Workspace:** one install and one lockfile for root tooling (commitlint, lefthook) and `web/`. This is not a monorepo, and the domain docs are single-context.
- Node 25+ no longer bundles corepack, so the Dockerfile runs `npm install -g pnpm@<version>` and CI installs Node and pnpm with mise.

### mise and pinned toolchains

mise installs Go, Node, pnpm, just, actionlint, and shellcheck from `mise.toml`. One tool manager covers the local build tools. CI reads the same Node, pnpm, actionlint, and shellcheck pins through `mise-action`. Go CI reads `go.mod`.

The Docker build images use exact patch versions and multi-arch digests. Digests prevent tag changes from silently changing the build tools. Renovate groups Node and Go updates across mise, manifests, and Docker. It also groups pnpm updates, including the Dockerfile's install command.

### TypeScript 7 + oxlint instead of ESLint

TypeScript 7 is the native (Go) compiler. `typescript-eslint` only supports TypeScript below 6.1, because it needs the old JS compiler API, so the usual ESLint setup can't run. oxlint needs no TypeScript API and is much faster. `tsc -b` still does full type-checking.

### `vendor/` is committed

Builds are offline and reproducible: the Docker build uses `-mod=vendor`, so it needs no network for Go modules. CI fails if `go.mod`, `go.sum` and `vendor/` drift apart. The cost is a larger clone and more noise in dependency PRs. Renovate runs `gomodVendor` after updates.

### Multi-arch images without emulation

The frontend and Go stages run on the build platform (`--platform=$BUILDPLATFORM`), and Go cross-compiles for each target. Building amd64 and arm64 costs little, and no QEMU is needed.

### Images on code merges to main

CI pushes `unstable`, `sha-<short>`, and `main` from runs on `main` that change code since the current `main` image. Runs on `main` share one concurrency group, so a burst of merges builds only the newest code: a merge whose waiting run is cancelled gets no `sha-` image of its own. The newest commit always gets an image. Skipping the runs in between saves runner time and hosted minutes. `main` and `unstable` move only forward. PRs never push: a PR builds an amd64 image only when build inputs change. See [CI cost](workflow.md#ci-cost). Releases push semver tags and `latest`. The GHCR package is public, so anyone can pull the images without signing in.

### CI on GitHub-hosted runners

Every CI job runs on `ubuntu-26.04`. A self-hosted runner on a public repo would run code from anyone's fork PR, and hosted runners are free for a public repo. `Image` and `release` publish what is deployed, and a clean VM for each build matters. `Image`, `Image Check`, and `release` use no build cache, so a build never reads a cache entry that another job wrote. The cache saved little, because the two Go compiles run on every build. The other jobs keep the Go, pnpm, and mise caches. See [CI cost](workflow.md#ci-cost).

### SQLite gotcha: no `STRICT` tables with timestamps

`STRICT` tables reject `TIMESTAMP`/`DATETIME` column types. The modernc driver (`_time_format=sqlite`) needs those declared types to scan columns as `time.Time`. Don't use `STRICT` on tables with time columns.
