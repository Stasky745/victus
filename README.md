# Victus

Self-hosted daily/weekly meal planner that tracks nutrition (calories, protein, iron, etc.) against
your own configurable goal ranges.

## Features

- **Day Builder** — pick meals per configurable category, live nutrient totals as you go.
- **Weekly Builder** — same, across 7 days, with per-day and weekly-average totals.
- **Configuration** — set per-nutrient goal ranges (min/max).
- **Meal Library** — manual entry, Open Food Facts search/barcode lookup, or import from Mealie/Tandoor.
- **Export / Import** — download your meal library, goals, and day-plan history as one JSON file from the Configuration page, and load it back into any Victus instance — including migrating between the Postgres and SQLite backends.

## Stack

Go + [chi](https://github.com/go-chi/chi) + [templ](https://templ.guide) + [htmx](https://htmx.org) + Tailwind CSS. Database is embedded SQLite (`modernc.org/sqlite`) by default — no separate service needed — or Postgres (`pgx`) via `DB_DRIVER=postgres` if you want a dedicated database server; both go through `sqlc`/`goose`. Auth is Victus's own username/password login by default (`AUTH_MODE=password`, no external dependency needed), or OIDC (`AUTH_MODE=oidc` — Pocket ID, Authentik, Keycloak, Authelia, or any standard provider) if you'd rather delegate to an existing IdP.

## Development

This repo uses [Nix flakes](https://nixos.wiki/wiki/Flakes) to pin the dev toolchain and
[lefthook](https://github.com/evilmartians/lefthook) for pre-commit checks.

```sh
nix develop            # drops you into a shell with go, templ, sqlc, goose, golangci-lint, lefthook, git-secrets
lefthook install       # one-time: wire up git hooks
git secrets --install && git secrets --register-aws   # one-time: secret-scanning hooks
while IFS= read -r p; do git secrets --add "$p"; done < .git-secrets-patterns  # one-time: extra patterns (API keys, session secrets, DSNs)
cp .env.example .env   # fill in required values
docker compose -f docker-compose.sqlite.yml up   # app only, embedded SQLite (the default)
```

Without Nix: install Go, [templ](https://templ.guide/quick-start/installation), [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html),
[goose](https://github.com/pressly/goose), and [lefthook](https://github.com/evilmartians/lefthook#installation) yourself.

### Common tasks

```sh
go generate ./...      # regenerate templ + sqlc code
tailwindcss -i web/static/input.css -o web/static/app.css --minify   # rebuild CSS after touching .templ or input.css
go run ./cmd/victus serve
go run ./cmd/victus migrate up
go test ./... -race
golangci-lint run
```

## Configuration

All configuration is via environment variables — see [`.env.example`](./.env.example) for the full list.

## Deployment

See [`docker-compose.sqlite.yml`](./docker-compose.sqlite.yml) (`docker compose -f docker-compose.sqlite.yml up`)
for the default single-container SQLite setup — no separate database service, good for
small/single-user installs — or [`docker-compose.yml`](./docker-compose.yml) for Postgres, if you
want a dedicated database server (better for multiple concurrent users). Put your own reverse
proxy (Caddy, Traefik, nginx, ...) in front for TLS; Victus trusts `X-Forwarded-*` headers when
`TRUST_PROXY_HEADERS=true`.

Neither backend encrypts its data files at rest on its own — if you need that, encrypt the
underlying volume/disk (LUKS, your cloud provider's encrypted disk option, etc.) rather than
relying on Victus or the database engine to do it.

### Container image (e.g. for Kubernetes)

Publishing a [GitHub Release](../../releases) (`.github/workflows/release.yaml`, triggered on
`release: published`) builds a multi-platform (`linux/amd64` + `linux/arm64`) image, scans it with
Trivy (fails the release on a CRITICAL/HIGH CVE that already has a fix available — one with no fix
yet doesn't block, since there'd be nothing actionable to do about it), and pushes it to GitHub
Container Registry at:

```
ghcr.io/<owner>/<repo>:latest        # always the most recent release
ghcr.io/<owner>/<repo>:<major.minor> # e.g. :1.2
ghcr.io/<owner>/<repo>:<version>     # e.g. :1.2.3, exact
```

(`<owner>/<repo>` is this repo's actual GitHub path, lowercased — e.g. if it lives at
`github.com/Stasky745/victus`, the image is `ghcr.io/stasky745/victus`.)

**First release only:** a brand-new GHCR package defaults to private, tied to the repo's
visibility. If your cluster pulls anonymously, go to the package's settings on GitHub and set it
to public (or configure an `imagePullSecret` in your cluster instead) — otherwise the pull will
fail with a 403/`ImagePullBackOff` despite the workflow having succeeded.

The image needs `DB_DRIVER`, `DATABASE_URL`, `SESSION_SECRET`, and (in password mode)
`ADMIN_EMAIL`/`ADMIN_PASSWORD` set (see [`.env.example`](./.env.example)) — for SQLite, mount a
volume at whatever path `DATABASE_URL` points to (the image runs as a non-root user, so that path
needs to be writable by it; a fresh `PersistentVolumeClaim` typically comes up root-owned — either
set `securityContext.fsGroup` on the pod so Kubernetes fixes ownership on mount, or add an init
container that `chown`s the volume before the app starts).
