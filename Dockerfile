# Taskiem: one binary that runs any role (spec 15.4), with the web app.
#
#   docker build -t taskiem:dev --build-arg VERSION=$(git describe --always) .
#
# Behind a TLS-inspecting proxy, pass its CA as a build secret (never stored
# in a layer) and the proxy as the usual build arguments:
#   docker build --secret id=extra_ca,src=/path/ca.pem --build-arg HTTPS_PROXY=... .

# Web app (M3e): built once, served by the API from /web.
FROM node:22-alpine AS web
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

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party third_party
RUN --mount=type=secret,id=extra_ca \
    if [ -s /run/secrets/extra_ca ]; then cat /etc/ssl/certs/ca-certificates.crt /run/secrets/extra_ca > /tmp/ca.pem; export SSL_CERT_FILE=/tmp/ca.pem; fi; \
    go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/taskiem ./cmd/taskiem
RUN mkdir -p /out/archive /out/anchors

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/taskiem /taskiem
COPY --from=web /src/web/dist /web
COPY --from=build --chown=nonroot:nonroot /out/archive /var/lib/taskiem/archive
COPY --from=build --chown=nonroot:nonroot /out/anchors /var/lib/taskiem/anchors
ENV TASKIEM_WEB_DIR=/web
USER nonroot:nonroot
EXPOSE 8080 8081 9090
ENTRYPOINT ["/taskiem"]
