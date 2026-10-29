# Tablero

A collaborative kanban board: workspaces with roles, boards, columns and tasks,
with optimistic concurrency and fractional-position ordering.

## Structure

- `backend/` — Go API with a hexagonal architecture ([decision 7](docs/DECISIONS.md)): `net/http`, PostgreSQL with pgx, migrations with goose, queries with sqlc.
  - `cmd/api` — composition root: builds the adapters and hands them to the use cases.
  - `internal/domain` — entities, rules, role matrix and domain errors (stdlib only).
  - `internal/app/{auth,workspace,board,task}` — use cases; each package defines its own ports.
  - `internal/adapters/http` — router, handlers and middlewares.
  - `internal/adapters/postgres` — repositories, `TxManager` and the sqlc-generated code (`db/`).
  - `internal/adapters/{token,hasher,events}` — JWT, argon2id and event publishing.
  - `internal/config` — configuration from environment variables.
  - `migrations/`, `queries/` — goose and sqlc SQL.
- `frontend/` — Next.js (App Router, TypeScript, Tailwind) with pnpm.
- `docs/` — [API (OpenAPI)](docs/openapi.yaml), [real-time event catalog](docs/EVENTS.md) and [architecture decisions](docs/DECISIONS.md).

## Requirements

- Go 1.26+
- Node 24+ and pnpm 11 (`corepack enable` or `npm i -g pnpm`)
- Docker with Docker Compose
- [goose](https://github.com/pressly/goose) and [sqlc](https://sqlc.dev):
  ```sh
  go install github.com/pressly/goose/v3/cmd/goose@latest
  go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
  ```
- `make` is optional: every `Makefile` target is a command you can run by hand.

## Everything with Docker

```sh
docker compose up --build        # or: docker compose up -d --build --wait
```

Starts, in order and each one waiting for the previous:

1. **db**: Postgres 16 (healthcheck with `pg_isready`).
2. **migrate**: applies `backend/migrations` with goose and exits. The API does not start if
   it fails (`service_completed_successfully`).
3. **api**: the API, once `/ready` responds (healthcheck).
4. **web**: the frontend (Next.js standalone server) at http://localhost:3000.

The images are multi-stage. The API is a static binary on distroless (no shell, `nonroot`
user) and the frontend runs as the `node` user. Host ports can be changed with
`DB_PORT`, `API_PORT` and `WEB_PORT`; Compose also reads the root `.env`, so its
`CORS_ORIGINS` and `JWT_SECRET` apply to the API container.
`docker compose down` stops the API without dropping requests (see *Health and shutdown*).

## Demo data

`cmd/seed` loads a sample workspace ready to show:

```sh
cd backend && go run ./cmd/seed                     # with DATABASE_URL in the environment
docker compose run --rm --entrypoint /app/seed api  # or against the Compose database
```

Users (public passwords, for the demo only):

| Person | Email | Password | Role in "Estudio Nube" |
|---|---|---|---|
| Ana Torres | `ana@estudionube.example` | `nube-admin-demo` | admin |
| Bruno Díaz | `bruno@estudionube.example` | `nube-miembro-demo` | member |
| Vera Molina | `vera@estudionube.example` | `nube-viewer-demo` | viewer (read-only) |

The **Estudio Nube** workspace has the board *Rediseño web · Panadería Trigo*: 4 columns
(Pendiente, En progreso, En revisión, Hecho), 21 cards (one archived) with labels,
priorities, dates relative to today (some overdue) and assignees, and 52 activity
entries (creations, moves and changes) spread over the last ten days. Everything is created
through the same use cases as the API, so the activity is what the app itself would produce.

It is idempotent: if the users exist, it restores their documented name, password and
role; if the workspace or the board exist, it reuses them (their ids and URLs don't
change) and resets the board **to its initial state**, deleting whatever was changed on it
and any other board in the workspace. It touches nothing outside "Estudio Nube".

### Demo mode

With `DEMO_MODE=true` the API loads the demo data on startup and also:

- The login page shows **"Entrar como invitado"** (`GET /demo` tells it whether demo mode is on).
  It creates a temporary account (`Invitado XXXX`) as a member of Estudio Nube, signs it in and
  opens the board. At most `DEMO_MAX_GUESTS` guests exist at once; beyond that it responds 429.
- Every `DEMO_RESET_INTERVAL` it deletes guests older than `DEMO_GUEST_TTL` and restores
  the demo data. Anyone with the board open sees it reload by itself (the stream sends
  a `resync`). With several instances, only one restores at a time (advisory lock).

| Variable | Default | |
|---|---|---|
| `DEMO_MODE` | `false` | Enables demo mode |
| `DEMO_RESET_INTERVAL` | `1h` | How often the data is restored (minimum `1m`) |
| `DEMO_GUEST_TTL` | `2h` | Age after which the next reset deletes a guest |
| `DEMO_MAX_GUESTS` | `100` | Maximum guests created within the TTL |

```sh
DEMO_MODE=true docker compose up -d --build --wait
```

The credentials in the table are public, the admin's included: on a deployment with
`DEMO_MODE` anyone can sign in as Ana. The reset undoes whatever they do, but don't enable
demo mode on a database with real data.

## Running the project for development

### 1. Configuration

```sh
cp .env.example .env
```

Replace `JWT_SECRET` with a random value of at least 32 bytes
(`openssl rand -base64 48`). The API validates everything on startup: if a required
variable is missing or any is invalid, it exits with code 2 and a list of every problem
(without echoing secret values), and if the database does not respond, it exits without
opening the port. Available variables:

| Variable | Default | |
|---|---|---|
| `DATABASE_URL` | — (required) | Postgres connection |
| `JWT_SECRET` | — (required, ≥ 32 bytes) | Signs the access tokens |
| `PORT` | `8080` | API port |
| `CORS_ORIGINS` | `http://localhost:3000` | Allowed origins, comma-separated |
| `ACCESS_TOKEN_TTL` | `15m` | Access token lifetime |
| `REFRESH_TOKEN_TTL` | `720h` | Refresh token lifetime |
| `COOKIE_SECURE` | `true` | `Secure` on the session cookies (works on `http://localhost`) |
| `COOKIE_SAMESITE` | `lax` | `lax`, `strict` or `none` (the latter only with `COOKIE_SECURE=true`) |
| `COOKIE_DOMAIN` | — | `Domain` of the session cookies; without it, only the API host |
| `TRUST_PROXY` | `false` | Behind a reverse proxy: client IP and scheme from `X-Forwarded-For` / `X-Forwarded-Proto` |
| `TRUST_PROXY_HOPS` | `1` | How many proxies add entries to `X-Forwarded-For` (2 with Vercel in front of Render) |
| `COOKIE_PATH_PREFIX` | — | Cookie path prefix when a proxy serves the API under a path (`/api` with the Vercel proxy) |
| `RATE_LIMIT` | `true` | Attempt limits on login, registration and guests |
| `SSE_MAX_PER_USER` | `10` | Open live streams per user (per instance) |
| `SSE_MAX_PER_BOARD` | `1000` | Open live streams per board (per instance) |
| `DEFAULT_WORKSPACE_ID` | — | Workspace every new account joins, as a **member**: can create, edit, move and delete tasks and manage labels, but not boards, columns, members or settings. The API does not start if it doesn't exist |
| `MIGRATE_ON_START` | `false` | Apply migrations on startup, before opening the port (for platforms without a pre-deploy step, see `docs/DEPLOY.md`) |
| `MIGRATIONS_DIR` / `MIGRATE_LOCK_TIMEOUT` | `migrations` / `5s` | Where the migrations live and how long one may wait for a table lock |
| `SHUTDOWN_DELAY` | `0s` | On SIGTERM, how long it keeps serving with `/ready` at 503 before it stops accepting connections |
| `SHUTDOWN_TIMEOUT` | `15s` | After that, how long in-flight requests may take before they are closed |
| `EVENTS_BACKEND` | `memory` | `memory` for a single instance; `postgres` for several instances on the same database (events and presence over `LISTEN/NOTIFY`, [decision 8](docs/DECISIONS.md)) |

### 2. Database

```sh
docker compose up -d --wait db   # Postgres 16 only, on localhost:5432
make migrate-up                  # or: goose -dir backend/migrations postgres "$DATABASE_URL" up
```

`make migrate-status` and `make migrate-down` show and roll back migrations.

### 3. API

The API reads its configuration from the environment (it does not load `.env` itself):

```sh
# bash / zsh
set -a; source .env; set +a
cd backend && go run ./cmd/api
```

```powershell
# PowerShell
Get-Content .env | Where-Object { $_ -match '^([A-Z_]+)=(.*)$' } | ForEach-Object { Set-Item "env:$($Matches[1])" $Matches[2] }
cd backend; go run ./cmd/api
```

Check that it responds: `curl http://localhost:8080/ready` → `{"status":"ok"}`.

### Health and shutdown

- `GET /health` is *liveness*: the process is alive. It doesn't query the database, so a
  Postgres outage doesn't restart a healthy API.
- `GET /ready` is *readiness*: the database responds and the API is not shutting down.
  Otherwise it responds 503 (`db_unavailable` or `shutting_down`).

On SIGTERM, `/ready` switches to 503 and the API keeps serving for `SHUTDOWN_DELAY`, so the
load balancer stops sending it traffic and already-queued connections get in. Then it stops
accepting connections and waits up to `SHUTDOWN_TIMEOUT` for in-flight requests; event
streams are closed and clients reconnect. In Compose that is 3 s + 15 s, within
`stop_grace_period: 20s`. A second signal terminates it immediately.

### 4. Frontend

```sh
cd frontend
cp .env.example .env.local       # NEXT_PUBLIC_API_URL=http://localhost:8080
pnpm install
pnpm dev                         # http://localhost:3000
```

## Tests

```sh
cd backend
go test ./...          # unit + integration (needs Docker)
go test -short ./...   # unit only
```

The use cases (`internal/app`) are tested with in-memory fakes of their ports.
The repositories (`internal/adapters/postgres`) and the full API
(`internal/adapters/http`) are tested against an ephemeral Postgres from
[Testcontainers](https://golang.testcontainers.org/), with the migrations applied.
To iterate faster against a server that is already running, point
`TEST_DATABASE_URL` at it: each test package creates its own database there, migrates it
and drops it when done.

```sh
TEST_DATABASE_URL="postgres://tablero:tablero@localhost:5432/tablero?sslmode=disable" go test ./...
```

### Frontend

```sh
cd frontend
pnpm test          # Vitest + Testing Library: board cache, forms and admin dialogs
pnpm test:e2e      # Playwright against the real API (below)
```

The e2e tests open two or three browsers with different people on the same board
(moving a card live, simultaneous edits with a conflict, a viewer without drag), plus
workspace administration (creating, inviting, private and archived boards).
They run against the app started with Docker Compose:

```sh
docker compose up -d --build --wait
cd frontend && pnpm exec playwright install chromium && pnpm test:e2e
```

They use the frontend already responding on port 3000 (the `web` container); if there is
none, Playwright builds and serves the one from the source. They create users and an
"E2E …" workspace through the API (with invitations). With other ports:
`API_PORT=8090 WEB_PORT=3100 CORS_ORIGINS=http://localhost:3100 docker compose up -d --build --wait`
and `E2E_API_URL=http://localhost:8090 E2E_PORT=3100 pnpm test:e2e`.

Lint: `golangci-lint run ./...` in `backend/` (includes `depguard`, which prevents
`domain` or `app` from importing adapters) and `pnpm lint` in `frontend/`.

## Code generation

After changing `backend/queries/*.sql` or the migrations, regenerate the data access
code with `sqlc generate` (in `backend/`) and commit `internal/adapters/postgres/db`.

`TestOpenAPIMatchesRouter` fails if a router route is missing from
`docs/openapi.yaml` or vice versa.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and on every pull request:

- **Backend:** `go vet`, `golangci-lint` (with the architecture dependency rules and gosec), `govulncheck`, tests (with Postgres as a service) and build.
- **Frontend:** `pnpm lint`, `pnpm audit --prod`, `pnpm test` and `pnpm build`.
- **E2E:** starts the whole app with `docker compose up --build --wait` (the API and frontend images), runs `pnpm test:e2e` against it and stops it with `docker compose stop`, checking that the API exits with code 0. On failure, it uploads the Playwright report and the logs.

## Status

Done: authentication (registration, login, refresh, logout, `/me`); workspaces with roles,
owner, address, settings and invitations by email or link; boards (templates, private,
archived, duplicated or moved between workspaces), columns and tasks with reordering,
optimistic concurrency, priority, due date, assignee, archiving, per-board labels and
an activity log.

Real time: `GET /boards/{id}/events` streams the board's changes over SSE
([decision 3](docs/DECISIONS.md)). To try it with curl, use the access token from
`/auth/login` (the browser uses the `access_token` cookie):

```sh
curl -N -H "Authorization: Bearer $TOKEN" http://localhost:8080/boards/$BOARD/events
```

The messages are documented in [`docs/EVENTS.md`](docs/EVENTS.md). `GET /metrics` exposes, in
Prometheus format, the SSE connections, published events, clients dropped for being
slow and delivery time. The load test (500 clients on one board while tasks are moved;
results in [decision 9](docs/DECISIONS.md)) runs with the API up:

```sh
cd backend && go run ./cmd/loadtest -url http://localhost:8080 -clients 500 -duration 30s -rate 10
```

All the test clients are the same user: start the API with
`SSE_MAX_PER_USER=1000` so the per-user stream limit doesn't cut it off.

## Deployment

The backend deploys for free on Render (`render.yaml`) with managed Postgres on Neon.
The variables, the migration strategy and the step-by-step guide are in [`docs/DEPLOY.md`](docs/DEPLOY.md).

## Security

The threat model, the defenses (rate limiting, CSRF, headers, cookies, limits) and
how to run `govulncheck`, gosec and `pnpm audit` are in [`docs/SECURITY.md`](docs/SECURITY.md).
