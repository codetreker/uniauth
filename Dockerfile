# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:24-alpine AS console
WORKDIR /src/web/console
COPY web/console/package.json web/console/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/console/index.html web/console/tsconfig.json web/console/vite.config.ts ./
COPY web/console/src ./src
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/console/embed.go ./web/console/embed.go
COPY --from=console /src/web/console/dist ./web/console/dist
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags="-s -w" -o /out/uniauth ./cmd/uniauth

FROM alpine:3.23
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 uniauth \
    && adduser -S -D -H -u 10001 -G uniauth uniauth \
    && mkdir /data \
    && chown 10001:10001 /data
COPY --from=build /out/uniauth /usr/local/bin/uniauth
USER 10001:10001
WORKDIR /data
ENV UNIAUTH_LISTEN_ADDR=:8080 \
    UNIAUTH_DATABASE_PATH=/data/uniauth.db
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/uniauth"]
CMD ["serve"]
