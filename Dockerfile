# Taskiem: one binary that runs any role (spec 15.4), with the web app.
#
#   docker build -t taskiem:dev --build-arg VERSION=$(git describe --always) .
#
# Behind a TLS-inspecting proxy, pass its CA as a build secret (never stored
# in a layer) and the proxy as the usual build arguments:
#   docker build --secret id=extra_ca,src=/path/ca.pem --build-arg HTTPS_PROXY=... .
#
# Base images are pinned by digest (the tag is kept for people and for
# Dependabot, which moves both), so CI's smoke tests and the signed release
# build from the same bases.

# Web app (M3e): built once, served by the API from /web.
FROM node:25-alpine@sha256:bdf2cca6fe3dabd014ea60163eca3f0f7015fbd5c7ee1b0e9ccb4ced6eb02ef4 AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY sdk/package.json sdk/
RUN --mount=type=secret,id=extra_ca \
    if [ -s /run/secrets/extra_ca ]; then export NODE_EXTRA_CA_CERTS=/run/secrets/extra_ca; fi; \
    pnpm install --frozen-lockfile
COPY web web
COPY sdk sdk
RUN pnpm --filter @taskiem/web build

FROM golang:1.26@sha256:670bb5aa5349ae03b8516764718486f4f16399134bdc9acd0c46a23f043ab05c AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party third_party
RUN --mount=type=secret,id=extra_ca \
    if [ -s /run/secrets/extra_ca ]; then cat /etc/ssl/certs/ca-certificates.crt /run/secrets/extra_ca > /tmp/ca.pem; export SSL_CERT_FILE=/tmp/ca.pem; fi; \
    go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/taskiem ./cmd/taskiem
# The container-step supervisor, copied into sandbox Pods by their init container (docs/container-steps.md).
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/taskiem-shim ./cmd/taskiem-shim
RUN mkdir -p /out/archive /out/anchors

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/taskiem /taskiem
COPY --from=build /out/taskiem-shim /taskiem-shim
COPY --from=web /src/web/dist /web
COPY --from=build /src/deploy/plans.yaml /etc/taskiem/plans.yaml
COPY --from=build --chown=nonroot:nonroot /out/archive /var/lib/taskiem/archive
COPY --from=build --chown=nonroot:nonroot /out/anchors /var/lib/taskiem/anchors
ENV TASKIEM_WEB_DIR=/web TASKIEM_BILLING_PLANS=/etc/taskiem/plans.yaml
USER nonroot:nonroot
EXPOSE 8080 8081 9090
ENTRYPOINT ["/taskiem"]
