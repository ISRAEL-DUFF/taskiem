# Web app (M3e): built once, served by the API from /web.
FROM node:22-alpine AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
COPY sdk/package.json sdk/
RUN pnpm install --frozen-lockfile
COPY web web
COPY sdk sdk
RUN pnpm --filter @taskiem/web build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party third_party
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/taskiem ./cmd/taskiem
RUN mkdir -p /out/archive

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/taskiem /taskiem
COPY --from=web /src/web/dist /web
COPY --from=build --chown=nonroot:nonroot /out/archive /var/lib/taskiem/archive
EXPOSE 8080 8081 9090
ENTRYPOINT ["/taskiem"]
