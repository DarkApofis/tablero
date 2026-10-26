# Despliegue del backend en Render

La API corre en Render como servicio web Docker, con un Postgres administrado de Render en la misma región (Frankfurt). Todo está declarado en [`render.yaml`](../render.yaml), un *Blueprint* de Render: el repositorio es la fuente de la configuración y cada cambio en él se aplica al sincronizar. El frontend se despliega aparte.

```
navegador ──HTTPS──▶ api.<tu dominio> (Render, TLS)
                        │
                        ▼
                tablero-api (Docker, 1 instancia, 0.5 CPU / 512 MB)
                        │ red privada
                        ▼
                tablero-db (Postgres 16 administrado, sin acceso desde internet)
```

## Qué hace cada pieza del despliegue

1. **Build:** Render construye `backend/Dockerfile`. Solo hace falta un deploy cuando cambia `backend/**` o `render.yaml` (`buildFilter`).
2. **Migraciones** (`preDeployCommand: /app/migrate`): se aplican en una instancia aparte, después del build y **antes** de arrancar la nueva versión. Mientras tanto la versión anterior sigue sirviendo. Si fallan, el deploy se cancela y no cambia nada.
3. **Arranque y health check:** la nueva instancia arranca. Render solo le pasa tráfico cuando `GET /health` responde 200. La API no abre el puerto hasta haber validado la configuración y alcanzado la base, así que «sano» implica «configurada y conectada».
4. **Relevo:** cuando la nueva instancia recibe tráfico, Render manda SIGTERM a la anterior (según su documentación, unos 60 s después). Esta pone `/ready` en 503, sigue atendiendo 5 s (`SHUTDOWN_DELAY`) y luego da hasta 20 s a las peticiones en curso (`SHUTDOWN_TIMEOUT`). Los streams SSE se cierran y los navegadores se reconectan solos a la nueva versión, recuperando lo que se perdieron con `Last-Event-ID`. Render manda SIGKILL a los 30 s (`maxShutdownDelaySeconds`), por encima de 5 + 20.
5. **Auto-deploy:** cada commit a la rama enlazada se despliega **cuando el CI pasa** (`autoDeployTrigger: checksPass`).

## Variables de entorno y secretos

| Variable | Valor | Tipo | Para qué |
|---|---|---|---|
| `DATABASE_URL` | URL privada de `tablero-db` | **Secreto**, lo inyecta Render (`fromDatabase`) | Conexión a Postgres por la red privada |
| `JWT_SECRET` | 256 bits aleatorios | **Secreto**, lo genera Render una vez (`generateValue`) | Firma de los access tokens. Rotarlo cierra todas las sesiones |
| `CORS_ORIGINS` | `https://app.<tu dominio>` | **Lo pides tú** al crear el Blueprint (`sync: false`) | Único origen que puede llamar a la API desde el navegador (CORS y CSRF) |
| `PORT` | `8080` | Fijo | Puerto de la API dentro del contenedor |
| `COOKIE_SECURE` | `true` | Fijo | Cookies de sesión solo por HTTPS |
| `COOKIE_SAMESITE` | `lax` | Fijo | `app.` y `api.` del mismo dominio son el mismo sitio |
| `TRUST_PROXY` | `true` | Fijo | IP del cliente y esquema desde las cabeceras del proxy de Render (se verifica en el paso 5) |
| `EVENTS_BACKEND` | `memory` | Fijo | Eventos en vivo en memoria: vale para una instancia |
| `SHUTDOWN_DELAY` / `SHUTDOWN_TIMEOUT` | `5s` / `20s` | Fijo | Apagado ordenado dentro de los 30 s de Render |
| `MIGRATE_LOCK_TIMEOUT` | `5s` | Fijo | Cuánto puede esperar una migración por un bloqueo de tabla antes de abortar el deploy |
| `RATE_LIMIT` | `true` | Fijo | Límites de login, registro e invitados |
| `DEMO_MODE` | `false` | Fijo | Sin cuentas de invitado ni reset de datos: es producción |
| `GOMEMLIMIT` | `400MiB` | Fijo | El recolector de Go se ajusta antes de llegar a los 512 MB de la instancia |

Los «fijos» viven en `render.yaml` y se cambian con un commit. Los secretos no están en el repositorio: Render los guarda y los muestra en *Environment* del servicio. Lo demás (`ACCESS_TOKEN_TTL`, `SSE_MAX_PER_USER`…) usa los valores por defecto del README.

## Migraciones: se aplican antes y son compatibles hacia atrás

Mientras corre `preDeployCommand` y durante el relevo, **la versión anterior de la API sigue sirviendo con el esquema nuevo**. Si un deploy falla después de migrar, o haces *rollback* desde Render, vuelve el código anterior, pero la base **no** se revierte. Por eso toda migración debe funcionar con la versión de la API que ya está en producción.

**Regla: *expand* y *contract* en releases distintos.**

| Cambio | Release N (expand) | Release N+1 o posterior (contract) |
|---|---|---|
| Columna nueva | `ADD COLUMN` que admite NULL o tiene `DEFAULT` | `SET NOT NULL` cuando el código ya la rellena siempre |
| Renombrar columna | Añadir la nueva; el código escribe en las dos y lee la nueva; copiar datos | Quitar la vieja |
| Quitar columna o tabla | El código deja de usarla | `DROP` |
| Cambiar tipo | Columna nueva con el tipo nuevo, como al renombrar | Quitar la vieja |
| Índice en tabla grande | `CREATE INDEX CONCURRENTLY` (migración con `-- +goose NO TRANSACTION`) | — |

**Salvaguardas automáticas:**

- **Test de compatibilidad** (`TestMigrationsAreBackwardCompatible`, en `backend/cmd/migrate`). Falla en el CI, y por tanto bloquea el auto-deploy, si la parte `Up` de una migración tiene `DROP`, `RENAME`, un cambio de tipo, `SET NOT NULL` o un `ADD COLUMN … NOT NULL` sin `DEFAULT`. Un *contract* legítimo se marca en esa sentencia con `-- compat: <por qué ya no lo usa ninguna versión en marcha>`.
- **Bloqueo de migraciones** (advisory lock de goose): dos deploys nunca migran a la vez.
- **`lock_timeout` de 5 s:** un `ALTER TABLE` que tiene que esperar a una consulta en curso aborta el deploy en vez de dejar en cola, detrás de él, todas las consultas de la API.
- **Transacción por migración** (por defecto en goose): una migración que falla no deja cambios a medias.

No hay *rollback* automático de esquema. Las secciones `Down` sirven para desarrollo local; en producción, un error se corrige con otra migración hacia delante.

## Health check, SSE y que la instancia no se duerma

- **Health check:** `/health` (liveness). Render lo usa para dar paso a la versión nueva y para reiniciar una instancia colgada. No se usa `/ready`, que consulta la base, para que una caída breve de Postgres no provoque reinicios de una API sana. `/ready` sigue disponible para quien quiera vigilar la base.
- **No se duerme:** el plan gratuito de Render duerme el servicio tras 15 minutos sin peticiones. Eso cortaría todos los streams SSE, y el siguiente visitante esperaría el arranque en frío. Por eso el servicio usa un plan de pago (`0.5c-512mb`), que está siempre encendido. Postgres también es de pago (`0.1c-256mb`), porque el gratuito caduca a las pocas semanas.
- **Conexiones largas:** el stream manda `: ping` cada 25 s, así que ningún proxy lo ve inactivo. Si Render corta una conexión (deploy, reinicio, límite de duración), el navegador reconecta con `Last-Event-ID` y recibe lo que se perdió, o un `resync`.
- **Una instancia:** `EVENTS_BACKEND=memory` reparte los eventos dentro de un solo proceso. Para escalar a varias, primero `EVENTS_BACKEND=postgres` (LISTEN/NOTIFY, decisión 8 de `DECISIONS.md`) y luego `numInstances`. Ten en cuenta que los límites de streams y de intentos son por instancia (`SECURITY.md`).

## Paso a paso

Los comandos los ejecutas tú. Para en cada paso y confirma antes de seguir.

### 0. Requisitos

- Cuenta en [Render](https://render.com) con un método de pago (los dos planes son de pago).
- El repositorio en GitHub. Render despliega desde un repo enlazado.
- Acceso al DNS de tu dominio, para `api.<tu dominio>`.

### 1. Subir el repositorio a GitHub

Revisa y haz commit de los archivos de despliegue y sube el repo. El nombre coincide con el del módulo Go (`github.com/DarkApofis/tablero`):

```sh
git add render.yaml docs/DEPLOY.md backend README.md
git commit -m "Despliegue en Render: Blueprint, migraciones en pre-deploy"
gh repo create DarkApofis/tablero --private --source . --push
```

Espera a que el CI pase en GitHub (pestaña *Actions*): el auto-deploy depende de ello.

### 2. Crear los servicios desde el Blueprint

En el dashboard de Render: **New → Blueprint**, conecta GitHub y elige `DarkApofis/tablero`. Render lee `render.yaml` y muestra `tablero-db` y `tablero-api`. Te pedirá:

- **`CORS_ORIGINS`:** `https://app.<tu dominio>`, exactamente el origen del frontend, sin barra final.

Pulsa **Apply**. Se crea la base, se construye la imagen, `preDeployCommand` aplica las 4 migraciones y arranca la API.

### 3. Comprobar el primer deploy

En **tablero-api → Events/Logs** deberías ver, en el *pre-deploy*:

```
"msg":"migrations up to date","applied":4
```

y después `"msg":"server starting"`. Con la URL que te da Render (`https://tablero-api-XXXX.onrender.com`):

```sh
curl -s https://tablero-api-XXXX.onrender.com/health   # {"status":"ok"}
curl -s https://tablero-api-XXXX.onrender.com/ready    # {"status":"ok"}: llega a la base
```

### 4. Dominio propio para la API

En **tablero-api → Settings → Custom Domains**, añade `api.<tu dominio>`. En tu proveedor de DNS crea el registro que te indique Render (un `CNAME` de `api` a `tablero-api-XXXX.onrender.com`). Render emite el certificado TLS solo. Comprueba:

```sh
curl -sI https://api.<tu dominio>/health | grep -iE "^HTTP|strict-transport|content-security"
```

Tiene que responder `HTTP/2 200`, con `Strict-Transport-Security` y `Content-Security-Policy`.

### 5. Verificar CORS, CSRF y la IP del cliente

```sh
# CORS: el origen del frontend recibe Access-Control-Allow-Origin; otro no.
curl -s -o /dev/null -D - -X OPTIONS https://api.<tu dominio>/auth/login \
  -H "Origin: https://app.<tu dominio>" -H "Access-Control-Request-Method: POST" | grep -i access-control-allow-origin
# CSRF: un origen ajeno recibe 403 csrf_rejected.
curl -s -X POST https://api.<tu dominio>/auth/logout -H "Origin: https://evil.example"
```

**IP del cliente (`TRUST_PROXY`).** El límite de intentos de login es por IP, y Render no documenta con claridad en qué posición de `X-Forwarded-For` pone la IP real. Hay que comprobarlo:

```sh
# 25 logins fallidos desde tu red: a partir del ~21 deben dar 429.
for i in $(seq 1 25); do curl -s -o /dev/null -w "%{http_code} " -X POST https://api.<tu dominio>/auth/login \
  -H "Content-Type: application/json" -d "{\"email\":\"prueba$i@example.com\",\"password\":\"x\"}"; done; echo
# Justo después, el mismo intento fingiendo otra IP: debe seguir dando 429 (no se puede falsificar).
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://api.<tu dominio>/auth/login -H "X-Forwarded-For: 203.0.113.9" \
  -H "Content-Type: application/json" -d '{"email":"prueba@example.com","password":"x"}'
```

Por último, desde otra red (los datos del móvil), un login fallido tiene que dar **401**, no 429. Si da 429, todos los clientes comparten la IP del proxy. Si el intento con `X-Forwarded-For` falso da 401, la IP se puede falsificar. En cualquiera de los dos casos, avísame y lo ajustamos.

### 6. Frontend

Al construir el frontend, apúntalo a la API (`NEXT_PUBLIC_API_URL=https://api.<tu dominio>`) y sírvelo en `https://app.<tu dominio>`, el mismo origen que pusiste en `CORS_ORIGINS`.

## Operación

- **Desplegar:** haz merge a la rama enlazada. Cuando el CI pasa, Render construye, migra y releva sin cortar el servicio.
- **Rollback:** en **tablero-api → Events**, *Rollback* a un deploy anterior. Vuelve el código, no el esquema, y por eso las migraciones son compatibles hacia atrás.
- **Logs:** JSON en **Logs**. Cada petición lleva su `request_id`, que también va en la cabecera `X-Request-ID` de la respuesta.
- **Backups:** los planes de pago de Render Postgres incluyen recuperación. Revisa en **tablero-db → Recovery** qué ventana cubre el plan elegido antes de tener datos reales.
- **Acceder a la base:** no admite conexiones desde internet (`ipAllowList: []`). Para un `psql` puntual, añade tu IP en **tablero-db → Networking** y quítala al terminar. La imagen distroless no tiene shell, así que la *Shell* de Render no sirve en la API.
- **Rotar `JWT_SECRET`:** cámbialo en **Environment** (*Generate*) y vuelve a desplegar. Cierra todas las sesiones.
