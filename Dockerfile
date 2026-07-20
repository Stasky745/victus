# Generated code (templ/sqlc) and compiled static assets (Tailwind CSS, vendored htmx)
# are committed to the repo, so the build stage only needs `go build` — no templ/sqlc/
# node/npm toolchain inside the image.
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/victus ./cmd/victus

FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates && \
    addgroup -S victus && adduser -S -G victus -H victus

COPY --from=builder /out/victus /usr/local/bin/victus

# /data is where docker-compose.sqlite.yml mounts its named volume for the
# SQLite file. A fresh named volume is seeded from whatever's at this path
# in the image at first mount (content *and* ownership) — without this, the
# volume comes up owned by root and the non-root victus user below can't
# open/create the database file in it (SQLITE_CANTOPEN).
RUN mkdir -p /data && chown victus:victus /data
VOLUME /data

USER victus
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/victus"]
CMD ["serve"]
