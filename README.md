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
- `docs/` — [API (OpenAPI)](docs/openapi.yaml), [catálogo de eventos en tiempo real](docs/EVENTS.md) y [decisiones de arquitectura](docs/DECISIONS.md).

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

## Todo con Docker

```sh
docker compose up --build        # o: docker compose up -d --build --wait
```

Levanta, en orden y cada uno esperando al anterior:

1. **db**: Postgres 16 (healthcheck con `pg_isready`).
2. **migrate**: aplica `backend/migrations` con goose y termina. La API no arranca si
   falla (`service_completed_successfully`).
3. **api**: la API, cuando `/ready` responde (healthcheck).
4. **web**: el frontend (servidor standalone de Next.js) en http://localhost:3000.

Las imágenes son multi-stage. La API es un binario estático sobre distroless (sin shell, usuario
`nonroot`) y el frontend corre con el usuario `node`. Los puertos del host se cambian con
`DB_PORT`, `API_PORT` y `WEB_PORT`; Compose también lee el `.env` de la raíz, así que su
`CORS_ORIGINS` y su `JWT_SECRET` se aplican al contenedor de la API.
`docker compose down` apaga la API sin cortar peticiones (ver *Salud y apagado*).

## Datos de demo

`cmd/seed` carga un workspace de ejemplo listo para enseñar:

```sh
cd backend && go run ./cmd/seed                     # con DATABASE_URL en el entorno
docker compose run --rm --entrypoint /app/seed api  # o contra la base de Compose
```

Usuarios (contraseñas públicas, solo para la demo):

| Persona | Correo | Contraseña | Rol en «Estudio Nube» |
|---|---|---|---|
| Ana Torres | `ana@estudionube.example` | `nube-admin-demo` | admin |
| Bruno Díaz | `bruno@estudionube.example` | `nube-miembro-demo` | miembro |
| Vera Molina | `vera@estudionube.example` | `nube-viewer-demo` | viewer (solo lectura) |

El workspace **Estudio Nube** tiene el tablero *Rediseño web · Panadería Trigo*: 4 columnas
(Pendiente, En progreso, En revisión, Hecho), 21 tarjetas (una archivada) con etiquetas,
prioridades, fechas relativas a hoy (algunas vencidas) y responsables, y 52 entradas de
actividad (altas, movimientos y cambios) repartidas por los últimos diez días. Todo se crea con los mismos casos de uso
que la API, así que la actividad es la que la app produciría.

Es idempotente: si los usuarios existen, les devuelve el nombre, la contraseña y el rol
documentados; si el workspace o el tablero existen, los reutiliza (sus ids y URLs no
cambian) y deja el tablero **como al principio**, borrando lo que se haya cambiado en él y
cualquier otro tablero del workspace. No toca nada fuera de «Estudio Nube».

### Modo demo

Con `DEMO_MODE=true` la API carga los datos de demo al arrancar y además:

- El login muestra **«Entrar como invitado»** (`GET /demo` le dice si está activo). Crea una
  cuenta temporal (`Invitado XXXX`) como miembro de Estudio Nube, inicia su sesión y abre el
  tablero. Como mucho hay `DEMO_MAX_GUESTS` invitados a la vez; luego responde 429.
- Cada `DEMO_RESET_INTERVAL` borra los invitados más antiguos que `DEMO_GUEST_TTL` y restaura
  los datos de demo. Quien tenga el tablero abierto lo ve recargarse solo (el stream le
  envía un `resync`). Con varias instancias, solo una restaura cada vez (advisory lock).

| Variable | Por defecto | |
|---|---|---|
| `DEMO_MODE` | `false` | Activa el modo demo |
| `DEMO_RESET_INTERVAL` | `1h` | Cada cuánto se restauran los datos (mínimo `1m`) |
| `DEMO_GUEST_TTL` | `2h` | Edad a partir de la cual el siguiente reset borra a un invitado |
| `DEMO_MAX_GUESTS` | `100` | Invitados creados dentro del TTL como máximo |

```sh
DEMO_MODE=true docker compose up -d --build --wait
```

Las credenciales de la tabla son públicas, también la del admin: en un despliegue con
`DEMO_MODE` cualquiera puede entrar como Ana. El reset deshace lo que haga, pero no actives
el modo demo en una base con datos reales.

## Levantar el proyecto para desarrollar

### 1. Configuración

```sh
cp .env.example .env
```

Cambia `JWT_SECRET` por un valor aleatorio de al menos 32 bytes
(`openssl rand -base64 48`). La API valida todo al arrancar: si falta una variable
obligatoria o alguna no es válida, termina con código 2 y una lista de todos los problemas
(sin repetir valores secretos), y si la base no responde, termina sin abrir el puerto.
Variables disponibles:

| Variable | Por defecto | |
|---|---|---|
| `DATABASE_URL` | — (obligatoria) | Conexión a Postgres |
| `JWT_SECRET` | — (obligatoria, ≥ 32 bytes) | Firma de los access tokens |
| `PORT` | `8080` | Puerto de la API |
| `CORS_ORIGINS` | `http://localhost:3000` | Orígenes permitidos, separados por comas |
| `ACCESS_TOKEN_TTL` | `15m` | Vida del access token |
| `REFRESH_TOKEN_TTL` | `720h` | Vida del refresh token |
| `COOKIE_SECURE` | `true` | `Secure` en la cookie del refresh token (funciona en `http://localhost`) |
| `SHUTDOWN_DELAY` | `0s` | Al recibir SIGTERM, cuánto sigue atendiendo con `/ready` en 503 antes de dejar de aceptar conexiones |
| `SHUTDOWN_TIMEOUT` | `15s` | Después, cuánto esperan las peticiones en curso antes de cerrarse |
| `EVENTS_BACKEND` | `memory` | `memory` para una instancia; `postgres` para varias instancias sobre la misma base (eventos y presencia por `LISTEN/NOTIFY`, [decisión 8](docs/DECISIONS.md)) |

### 2. Base de datos

```sh
docker compose up -d --wait db   # solo Postgres 16, en localhost:5432
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

Comprueba que responde: `curl http://localhost:8080/ready` → `{"status":"ok"}`.

### Salud y apagado

- `GET /health` es *liveness*: el proceso está vivo. No consulta la base, así que una
  caída de Postgres no hace reiniciar una API sana.
- `GET /ready` es *readiness*: la base responde y la API no se está apagando. Si no,
  responde 503 (`db_unavailable` o `shutting_down`).

Con SIGTERM, `/ready` pasa a 503 y la API sigue atendiendo `SHUTDOWN_DELAY`, para que el
balanceador deje de enviarle tráfico y entren las conexiones ya encoladas. Después deja de
aceptar conexiones y espera hasta `SHUTDOWN_TIMEOUT` a las peticiones en curso; los
streams de eventos se cierran y los clientes se reconectan. En Compose son 3 s + 15 s,
dentro de `stop_grace_period: 20s`. Una segunda señal la termina en el acto.

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

### Frontend

```sh
cd frontend
pnpm test          # Vitest + Testing Library: caché del tablero y formulario de login
pnpm test:e2e      # Playwright contra la API real (abajo)
```

Los e2e abren dos o tres navegadores con personas distintas en el mismo tablero
(mover una tarjeta en vivo, edición simultánea con conflicto, viewer sin arrastre).
Corren contra la aplicación levantada con Docker Compose:

```sh
docker compose up -d --build --wait
cd frontend && pnpm exec playwright install chromium && pnpm test:e2e
```

Usan el frontend que ya responde en el puerto 3000 (el contenedor `web`); si no hay
ninguno, Playwright construye y sirve el del código. Crean usuarios y un workspace
«E2E …» en la base. Con otros puertos:
`API_PORT=8090 WEB_PORT=3100 CORS_ORIGINS=http://localhost:3100 docker compose up -d --build --wait`
y `E2E_API_URL=http://localhost:8090 E2E_PORT=3100 pnpm test:e2e` (más
`E2E_DATABASE_URL` si cambias `DB_PORT`).

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
- **Frontend:** `pnpm lint`, `pnpm test` y `pnpm build`.
- **E2E:** levanta toda la aplicación con `docker compose up --build --wait` (las imágenes de la API y del frontend), corre `pnpm test:e2e` contra ella y la para con `docker compose stop`, comprobando que la API termina con código 0. Si falla, sube el informe de Playwright y los logs.

## Estado

Hecho: autenticación (registro, login, `/me`), workspaces y miembros con roles,
boards, columnas y tareas con reordenamiento, concurrencia optimista, prioridad,
fecha límite, responsable, archivado, etiquetas por tablero y registro de actividad.

Tiempo real: `GET /boards/{id}/events` emite por SSE los cambios del tablero
([decisión 3](docs/DECISIONS.md)). Para probarlo con curl, usa el access token de
`/auth/login` (el navegador usa la cookie `access_token`):

```sh
curl -N -H "Authorization: Bearer $TOKEN" http://localhost:8080/boards/$BOARD/events
```

Los mensajes están en [`docs/EVENTS.md`](docs/EVENTS.md). `GET /metrics` expone en
formato Prometheus las conexiones SSE, los eventos publicados, los clientes expulsados
por lentos y el tiempo de entrega. La prueba de carga (500 clientes sobre un board
mientras se mueven tareas; resultados en la [decisión 9](docs/DECISIONS.md)) se corre
con la API levantada:

```sh
cd backend && go run ./cmd/loadtest -url http://localhost:8080 -clients 500 -duration 30s -rate 10
```

Pendiente: refresco y cierre de sesión (`/auth/refresh`, `/auth/logout`), invitar
miembros y la interfaz del
tablero en el frontend.
