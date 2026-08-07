# syntax=docker/dockerfile:1.8

FROM caddy:2.10.2-builder-alpine AS builder
RUN xcaddy build v2.10.2 --with github.com/mholt/caddy-ratelimit@v0.1.0

FROM caddy:2.10.2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
