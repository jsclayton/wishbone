# Trademarks

The code in this repo is under the [AGPL-3.0](LICENSE). The AGPL-3.0 covers the code only. It does not give you any right to the name "Wishbone" or to the Wishbone logo (`docs/images/wishbone-logo.png`).

"Wishbone" and the logo are not registered trademarks. This file states how the maintainer asks them to be used, so that forks and repackaged builds aren't mistaken for this project.

## What this covers

- The name "Wishbone".
- The logo: the artwork of two Go gophers pulling a wishbone, in [`docs/images/wishbone-logo.png`](docs/images/wishbone-logo.png) and its smaller copy `docs/images/wishbone-logo-640.png`.

The app icons in `web/public/` (`favicon.svg`, `apple-touch-icon.png`, `icon-192.png`, `icon-512.png`, and `icon-maskable-512.png`) are not covered. They are a plain wishbone shape, not the logo. A fork may keep them or change them.

The logo artwork is © John Clayton, all rights reserved, except the Go gopher, which is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). The gopher was designed by Renée French. This policy does not cover gophers in general. It covers only this artwork and the name.

## Forks and repackaged builds

If you fork Wishbone, or publish a changed or repackaged build of it:

- Don't use the name "Wishbone" or the logo as your own name or branding. That means the product name shown to users, the logo, and the name of the image you publish. Remove the logo files (`docs/images/wishbone-logo.png` and `docs/images/wishbone-logo-640.png`, which the README shows) and use another name.
- Don't suggest that your build is the official Wishbone, or that the maintainer made, approved, or supports it.
- Keep all copyright and license notices, as the AGPL-3.0 requires.

To rename a fork, change the name in these places:

- `web/index.html`: the `<title>` and the `apple-mobile-web-app-title` meta tag.
- `web/public/manifest.webmanifest`: `name` and `short_name`.
- `web/src/Screen.tsx`: the header text on the left of the top row.
- The other screens' text that says "Wishbone". Search `web/src` for it.
- The image name in `docker-compose.yml`. The workflows publish under the name of the repository they run in.

The "Source code" link in Menu points to this repo. A fork changes `sourceUrl` in `web/src/source.ts` to its own repository, so the link leads to the source that the fork runs.

You may say that your project is "based on Wishbone", and refer to this project by name and link to this repo.

The Go module path `jsclayton.dev/wishbone` is a technical identifier, not branding. A fork may keep it or change it.

## Questions

Open an issue if you are not sure whether a use is fine.
