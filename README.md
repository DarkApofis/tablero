# Tablero

Tablero kanban colaborativo: workspaces con roles, tableros, columnas y tareas
con concurrencia optimista y orden por posición fraccionaria.

## Estructura

- `backend/` — API en Go con arquitectura hexagonal ([decisión 7](docs/DECISIONS.md)): `net/http`, PostgreSQL con pgx, migraciones con goose, consultas con sqlc.
  - `cmd/api` — raíz de composición: construye los adaptadores y los pasa a los casos de uso.
  - `internal/domain` — entidades, reglas, matriz de roles y errores de dominio (solo stdlib).
  - `internal/app/{auth,workspace,board,task}` — casos de uso; cada paquete define sus puertos.
  - `internal/adapters/http` — router, handlers y middlewares.
  - `internal/adapters/postgres` — repositorios, `TxManager` y el código generado por sqlc (`db/`).
  - `internal/adapters/{token,hasher,events}` — JWT, argon2id y publicación de eventos.
  - `internal/config` — configuración desde variables de entorno.
  - `migrations/`, `queries/` — SQL de goose y de sqlc.
- `frontend/` — Next.js (App Router, TypeScript, Tailwind) con pnpm.
- `docs/` — [API (OpenAPI)](docs/openapi.yaml) y [decisiones de arquitectura](docs/DECISIONS.md).

## Requisitos

- Go 1.26+
- Node 24+ y pnpm 11 (`corepack enable` o `npm i -g pnpm`)
- Docker con Docker Compose
- [goose](https://github.com/pressly/goose) y [sqlc](https://sqlc.dev):
  ```sh
  go install github.com/pressly/goose/v3/cmd/goose@latest
  go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
  ```
- `make` es opcional: cada target del `Makefile` es un comando que puedes ejecutar a mano.

## Levantar el proyecto

### 1. Configuración

```sh
cp .env.example .env
```

Cambia `JWT_SECRET` por un valor aleatorio de al menos 32 bytes
(`openssl rand -base64 48`). Variables disponibles:

| Variable | Por defecto | |
|---|---|---|
| `DATABASE_URL` | — (obligatoria) | Conexión a Postgres |
| `JWT_SECRET` | — (obligatoria, ≥ 32 bytes) | Firma de los access tokens |
| `PORT` | `8080` | Puerto de la API |
| `CORS_ORIGINS` | `http://localhost:3000` | Orígenes permitidos, separados por comas |
| `ACCESS_TOKEN_TTL` | `15m` | Vida del access token |
| `REFRESH_TOKEN_TTL` | `720h` | Vida del refresh token |
| `COOKIE_SECURE` | `true` | `Secure` en la cookie del refresh token (funciona en `http://localhost`) |

### 2. Base de datos

```sh
docker compose up -d --wait      # Postgres 16 en localhost:5432
make migrate-up                  # o: goose -dir backend/migrations postgres "$DATABASE_URL" up
```

`make migrate-status` y `make migrate-down` muestran y revierten migraciones.

### 3. API

La API lee la configuración del entorno (no carga `.env` por sí misma):

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

Comprueba que responde: `curl http://localhost:8080/health` → `{"status":"ok"}`.

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
go test ./...          # unitarios + integración (necesita Docker)
go test -short ./...   # solo unitarios
```

Los casos de uso (`internal/app`) se prueban con fakes en memoria de sus puertos.
Los repositorios (`internal/adapters/postgres`) y la API completa
(`internal/adapters/http`) se prueban contra un Postgres efímero de
[Testcontainers](https://golang.testcontainers.org/), al que se le aplican las
migraciones. Para iterar más rápido contra un servidor ya levantado, apunta
`TEST_DATABASE_URL` a él: cada paquete de tests crea ahí su propia base, la migra
y la borra al terminar.

```sh
TEST_DATABASE_URL="postgres://tablero:tablero@localhost:5432/tablero?sslmode=disable" go test ./...
```

Lint: `golangci-lint run ./...` en `backend/` (incluye `depguard`, que impide que
`domain` o `app` importen adaptadores) y `pnpm lint` en `frontend/`.

## Generación de código

Tras cambiar `backend/queries/*.sql` o las migraciones, regenera el código de acceso
a datos con `sqlc generate` (en `backend/`) y haz commit de `internal/adapters/postgres/db`.

`TestOpenAPIMatchesRouter` falla si una ruta del router no está en
`docs/openapi.yaml` o viceversa.

## CI

`.github/workflows/ci.yml` corre en cada push a `main` y en cada pull request:

- **Backend:** `go vet`, `golangci-lint` (con las reglas de dependencias de la arquitectura), tests (con Postgres como servicio) y build.
- **Frontend:** `pnpm lint` y `pnpm build`.

## Estado

Hecho: autenticación (registro, login, `/me`), workspaces y miembros con roles,
boards, columnas y tareas con reordenamiento, concurrencia optimista, prioridad,
fecha límite, responsable, archivado, etiquetas por tablero y registro de actividad.

Pendiente: refresco y cierre de sesión (`/auth/refresh`, `/auth/logout`), invitar
miembros, tiempo real con SSE ([decisión 3](docs/DECISIONS.md)) y la interfaz del
tablero en el frontend.
