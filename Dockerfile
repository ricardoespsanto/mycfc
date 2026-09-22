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
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/media-cleanup ./cmd/media-cleanup \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/data-retention ./cmd/data-retention \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/guardian-activation ./cmd/guardian-activation

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/mycfc /app/mycfc
COPY --from=build /out/media-cleanup /app/media-cleanup
COPY --from=build /out/data-retention /app/data-retention
COPY --from=build /out/guardian-activation /app/guardian-activation
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/mycfc"]
