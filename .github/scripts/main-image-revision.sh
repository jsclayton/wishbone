#!/usr/bin/env bash
# Prints the commit that the `main` image on GHCR was built from: the
# org.opencontainers.image.revision label of its linux/amd64 config.
# Exits non-zero if it can't read a full commit SHA.
#
# Env: IMAGE (owner/name) and GITHUB_TOKEN (needs packages: read).
# Used by the `Changes` and `Image` jobs in .github/workflows/ci.yml.
set -euo pipefail

# Credentials go to curl on stdin (-K-), never in argv, so `ps` can't show them.
get() {
  curl -fsSL --max-time 30 -K- -H "Accept: $1" "https://ghcr.io/v2/$IMAGE/$2" \
    <<<"header = \"Authorization: Bearer $token\""
}

token=$(curl -fsS --max-time 30 -K- \
  "https://ghcr.io/token?scope=repository:$IMAGE:pull&service=ghcr.io" \
  <<<"user = \"token:$GITHUB_TOKEN\"" | jq -er .token)
digest=$(get application/vnd.oci.image.index.v1+json manifests/main |
  jq -er 'first(.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64") | .digest)')
config=$(get application/vnd.oci.image.manifest.v1+json "manifests/$digest" | jq -er .config.digest)
revision=$(get application/vnd.oci.image.config.v1+json "blobs/$config" |
  jq -r '.config.Labels["org.opencontainers.image.revision"] // empty')

if ! [[ "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  # The label is untrusted: print only a short, safe slice of it.
  safe=$(printf '%s' "$revision" | tr -cd '[:alnum:]._-' | cut -c1-64)
  echo "The main image has no commit label (got '$safe')." >&2
  exit 1
fi
echo "$revision"
