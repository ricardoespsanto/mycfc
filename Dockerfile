# syntax=docker/dockerfile:1.8

FROM node:24.20-alpine3.24 AS assets
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY scripts/build-assets.mjs scripts/build-assets.mjs
COPY ui/static/src ui/static/src
RUN npm run build

FROM golang:1.27.1-alpine3.24 AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY docs/legal ./docs/legal
COPY internal ./internal
COPY ui ./ui
COPY --from=assets /src/ui/static/dist ./ui/static/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mycfc ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/privacy-restore-replay ./cmd/privacy-restore-replay \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/privacy-retention ./cmd/privacy-retention

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/mycfc /app/mycfc
COPY --from=build /out/privacy-restore-replay /app/privacy-restore-replay
COPY --from=build /out/privacy-retention /app/privacy-retention
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/mycfc"]
