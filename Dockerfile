# syntax=docker/dockerfile:1

# Frontend and Go build stages run on the build host's platform and
# cross-compile, so multi-arch images need no emulation.

FROM --platform=$BUILDPLATFORM node:26.13.0-trixie-slim@sha256:e4db4c914bf8ad241bd24804fc25d272d89bad97e68cdc8a1414862790a02c39 AS web
WORKDIR /src
# Node 25+ no longer bundles corepack.
RUN npm install -g pnpm@12.16.1
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
# --ignore-scripts skips the root "prepare" (git hooks) script.
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts
COPY web/ web/
RUN pnpm --filter web build

FROM --platform=$BUILDPLATFORM golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385 AS go
ARG TARGETOS TARGETARCH
ARG VERSION=dev COMMIT=unknown BUILD_TIME=unknown
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/web/dist internal/server/web/dist
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath \
      -ldflags="-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildTime=${BUILD_TIME}'" \
      -o /out/wishbone ./cmd/wishbone && \
    mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=go /out/wishbone /app/wishbone
COPY --from=go --chown=nonroot:nonroot /out/data /data
ENV WISHBONE_DATA_DIR=/data \
    WISHBONE_PORT=8080
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/app/wishbone"]
CMD ["serve"]
