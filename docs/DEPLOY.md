# Despliegue del backend (gratis): Render + Neon

La API corre en Render como servicio web Docker en una **instancia gratuita**. La base de datos es **Neon**, un Postgres administrado con plan gratuito permanente. Las dos están en Frankfurt. La API está declarada en [`render.yaml`](../render.yaml), un *Blueprint* de Render: el repositorio es la fuente de la configuración. El frontend se despliega aparte.

```
navegador ──HTTPS──▶ api.<tu dominio> (Render, TLS)          monitor externo ──cada 10 min──▶ /health
                        │
                        ▼
                tablero-api (Docker, instancia gratuita 0.1 CPU / 512 MB, Frankfurt)
                        │ TLS
                        ▼
                Neon (Postgres 16 gratuito, AWS Frankfurt)
```

## Qué implica el plan gratuito

| | Gratis (esta configuración) | De pago (al final de esta guía) |
|---|---|---|
| Migraciones | La API las aplica al arrancar, antes de abrir el puerto (`MIGRATE_ON_START`) | Paso de pre-deploy aparte (`preDeployCommand`) |
| Dormirse | Tras 15 min sin peticiones, salvo que un monitor la mantenga despierta | Siempre encendida |
| Base | Neon: 1 GB, se suspende tras 5 min sin uso, restauración de las últimas 6 h | Render Postgres en la red privada, con copias |
| CPU | 0,1 CPU: un login (argon2id) tarda bastante más que en local | 0,5 CPU o más |
| Horas | 750 h al mes por workspace. Este servicio despierto 24/7 gasta ~744 h, así que no caben otros servicios gratuitos | Sin límite |

Fuentes: [Render, plan gratuito](https://render.com/docs/free), [Neon, precios](https://neon.com/pricing).

## Qué hace cada pieza del despliegue

1. **Build:** Render construye `backend/Dockerfile`. Solo hace falta un deploy cuando cambia `backend/**` o `render.yaml` (`buildFilter`).
2. **Arranque de la nueva versión:**
   - valida la configuración;
   - aplica las migraciones pendientes;
   - se conecta a la base;
   - y solo entonces abre el puerto.
   
   Si algo falla, el proceso termina, el health check nunca pasa y el deploy se da por fallido.
3. **Health check:** Render pasa el tráfico a la versión nueva cuando `GET /health` responde 200.
4. **Apagado:** la versión anterior recibe SIGTERM. Pone `/ready` en 503, sigue atendiendo 5 s y da hasta 20 s a las peticiones en curso. Los streams SSE se cierran, y los navegadores reconectan solos y recuperan lo perdido con `Last-Event-ID`.
5. **Auto-deploy:** cada commit a `main` se despliega cuando el CI pasa (`autoDeployTrigger: checksPass`).

## Variables de entorno y secretos

| Variable | Valor | Tipo | Para qué |
|---|---|---|---|
| `DATABASE_URL` | URL **directa** de Neon + `&pool_max_conn_idle_time=4m` | **Secreto**, lo pones tú (`sync: false`) | Conexión a Postgres. Directa porque las migraciones usan un advisory lock de sesión, que el pooler de Neon (PgBouncer en modo transacción) no mantiene. El `pool_max_conn_idle_time` cierra las conexiones ociosas para que Neon pueda suspenderse y no gastar horas de cómputo |
| `JWT_SECRET` | 256 bits aleatorios | **Secreto**, lo genera Render una vez (`generateValue`) | Firma de los access tokens. Rotarlo cierra todas las sesiones |
| `CORS_ORIGINS` | `https://app.<tu dominio>` | Lo pones tú (`sync: false`) | Único origen que puede llamar a la API desde el navegador (CORS y CSRF) |
| `MIGRATE_ON_START` | `true` | Fijo | Migrar al arrancar: el plan gratuito no tiene pre-deploy |
| `MIGRATE_LOCK_TIMEOUT` | `5s` | Fijo | Cuánto puede esperar una migración por un bloqueo de tabla antes de fallar |
| `PORT` | `8080` | Fijo | Puerto de la API dentro del contenedor |
| `COOKIE_SECURE` | `true` | Fijo | Cookies de sesión solo por HTTPS |
| `COOKIE_SAMESITE` | `lax` | Fijo | `app.` y `api.` del mismo dominio son el mismo sitio |
| `TRUST_PROXY` | `true` | Fijo | IP del cliente y esquema desde las cabeceras del proxy de Render (se verifica en el paso 6) |
| `EVENTS_BACKEND` | `memory` | Fijo | Eventos en vivo en memoria: una sola instancia |
| `SHUTDOWN_DELAY` / `SHUTDOWN_TIMEOUT` | `5s` / `20s` | Fijo | Apagado ordenado |
| `RATE_LIMIT` | `true` | Fijo | Límites de login, registro e invitados |
| `DEMO_MODE` | `false` | Fijo | Producción: sin invitados ni reset de datos |
| `GOMEMLIMIT` | `400MiB` | Fijo | El recolector de Go se ajusta antes de llegar a los 512 MB |

Los «fijos» viven en `render.yaml` y se cambian con un commit. Los secretos no están en el repositorio: Render los guarda en **Environment**. Lo demás usa los valores por defecto del README.

## Migraciones: se aplican antes y son compatibles hacia atrás

La versión nueva aplica las migraciones **antes** de aceptar tráfico, pero la versión anterior **sigue sirviendo** con el esquema nuevo hasta el relevo. Además, un *rollback* desde Render devuelve el código anterior, pero no revierte la base. Por eso toda migración tiene que funcionar con la versión de la API que ya está en producción.

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
- **Bloqueo de migraciones** (advisory lock de goose): dos arranques nunca migran a la vez.
- **`lock_timeout` de 5 s:** un `ALTER TABLE` que tiene que esperar a una consulta en curso falla en vez de dejar en cola, detrás de él, las consultas de la versión que sirve. La versión nueva no arranca, el deploy falla y la anterior sigue.
- **Transacción por migración** (por defecto en goose): una migración que falla no deja cambios a medias.

No hay *rollback* automático de esquema. Las secciones `Down` sirven para desarrollo local; en producción, un error se corrige con otra migración hacia delante.

## Health check, SSE y que la instancia no se duerma

- **Health check:** `/health` (liveness). No consulta la base, por dos motivos: una caída breve de Neon no debe provocar reinicios, y el monitor no debe despertar a Neon cada 10 minutos. La API no abre el puerto sin haber alcanzado la base, así que pasar el health check del deploy ya implica conexión.
- **No dormirse:**
  - Render duerme una instancia gratuita tras 15 minutos sin tráfico entrante. Cuenta las peticiones HTTP y los mensajes de WebSocket, pero no dice nada de SSE. Un stream abierto, en el que el cliente no envía nada, probablemente no cuente.
  - Por eso un monitor externo gratuito llama a `/health` cada 10 minutos (paso 5). Sin él, el siguiente visitante esperaría alrededor de un minuto y los streams abiertos se cortarían; al despertar se reconectan solos.
  - Render no prohíbe ni documenta esta práctica. Si algún día lo hace, la alternativa es el plan de pago.
- **Neon sí se duerme**, y está bien:
  - Se suspende tras 5 minutos sin consultas, y la primera consulta después tarda algo más.
  - Mientras alguien tiene un tablero abierto, el stream comprueba sus permisos cada 25 s y Neon sigue despierto.
  - Sin nadie conectado, la API cierra sus conexiones ociosas a los 4 minutos y Neon se suspende.
- **Conexiones largas:** el stream manda `: ping` cada 25 s, así que ningún proxy lo ve inactivo.
- **Una instancia:** el plan gratuito no escala, y `EVENTS_BACKEND=memory` asume un solo proceso.

## Paso a paso

Los comandos y los clics los haces tú. Para en cada paso y confirma antes de seguir.

### 1. Repositorio en GitHub ✓

Hecho: `DarkApofis/tablero`, con el CI en verde.

### 2. Crear la base en Neon

1. Crea una cuenta en [neon.com](https://neon.com). No pide tarjeta.
2. **New Project:** nombre `tablero`, Postgres **16** (el que usan Compose y el CI), región **AWS Europe Central 1 (Frankfurt)**.
3. En **Connect**, elige la rama `production` (o `main`), la base `neondb` (o crea `tablero`) y **desactiva «Connection pooling»**. La URL directa no lleva `-pooler` en el host.
4. Copia la URL y añádele al final `&pool_max_conn_idle_time=4m`. Queda así:

   ```
   postgresql://<usuario>:<contraseña>@ep-xxxx.eu-central-1.aws.neon.tech/<base>?sslmode=require&pool_max_conn_idle_time=4m
   ```

   Si Neon añade también `&channel_binding=require`, déjalo.

Guárdala; es el valor de `DATABASE_URL` en el paso siguiente. No la pegues en el chat ni en el repositorio.

### 3. Crear el servicio desde el Blueprint

En [dashboard.render.com](https://dashboard.render.com): **New → Blueprint**, conecta GitHub y elige `DarkApofis/tablero`, rama `main`. Render lee `render.yaml` y muestra **tablero-api** con el plan **Free**. Te pedirá:

- **`DATABASE_URL`:** la URL del paso 2.
- **`CORS_ORIGINS`:** `https://app.<tu dominio>`, con `https://` y sin barra final.

Pulsa **Apply**.

### 4. Comprobar el primer deploy

En **tablero-api → Logs** deberías ver:

```
"msg":"migrations up to date","applied":4
"msg":"database connected"
"msg":"server starting","addr":":8080"
```

Y con la URL que te da Render:

```sh
curl -s https://tablero-api-XXXX.onrender.com/health   # {"status":"ok"}
curl -s https://tablero-api-XXXX.onrender.com/ready    # {"status":"ok"}: llega a Neon
```

### 5. Monitor para que no se duerma

En [UptimeRobot](https://uptimerobot.com) (o [cron-job.org](https://cron-job.org)), crea un monitor gratuito:

- **Tipo:** HTTP(s)
- **URL:** `https://tablero-api-XXXX.onrender.com/health`. Cuando tengas el dominio del paso 6, cámbiala a `https://api.<tu dominio>/health`.
- **Intervalo:** 10 minutos
- **Alerta:** tu correo. Así también te enteras si la API se cae.

### 6. Dominio propio para la API y verificación

En **tablero-api → Settings → Custom Domains**, añade `api.<tu dominio>`. En tu proveedor de DNS crea el registro que te indique Render (un `CNAME` de `api` a `tablero-api-XXXX.onrender.com`). Render emite el certificado TLS solo.

```sh
curl -sI https://api.<tu dominio>/health | grep -iE "^HTTP|strict-transport|content-security"
# CORS: el origen del frontend recibe Access-Control-Allow-Origin
curl -s -o /dev/null -D - -X OPTIONS https://api.<tu dominio>/auth/login \
  -H "Origin: https://app.<tu dominio>" -H "Access-Control-Request-Method: POST" | grep -i access-control-allow-origin
# CSRF: un origen ajeno recibe 403 csrf_rejected
curl -s -X POST https://api.<tu dominio>/auth/logout -H "Origin: https://evil.example"
```

**IP del cliente (`TRUST_PROXY`).** El límite de intentos de login es por IP, y Render no documenta con claridad en qué posición de `X-Forwarded-For` pone la IP real. Hay que comprobarlo:

```sh
# 25 logins fallidos desde tu red: a partir del ~21 deben dar 429.
for i in $(seq 1 25); do curl -s -o /dev/null -w "%{http_code} " -X POST https://api.<tu dominio>/auth/login \
  -H "Content-Type: application/json" -d "{\"email\":\"prueba$i@example.com\",\"password\":\"x\"}"; done; echo
# Justo después, fingiendo otra IP: debe seguir dando 429 (no se puede falsificar).
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://api.<tu dominio>/auth/login -H "X-Forwarded-For: 203.0.113.9" \
  -H "Content-Type: application/json" -d '{"email":"prueba@example.com","password":"x"}'
```

Por último, desde otra red (los datos del móvil), un login fallido tiene que dar **401**, no 429. Si da 429, todos comparten la IP del proxy. Si el intento con `X-Forwarded-For` falso da 401, la IP se puede falsificar. En cualquiera de los dos casos hay que ajustarlo.

### 7. Frontend

Al construir el frontend, apúntalo a la API (`NEXT_PUBLIC_API_URL=https://api.<tu dominio>`) y sírvelo en `https://app.<tu dominio>`, el mismo origen que pusiste en `CORS_ORIGINS`.

## Operación

- **Desplegar:** haz merge a `main`. Cuando el CI pasa, Render construye y la nueva versión migra, arranca y releva a la anterior.
- **Rollback:** en **tablero-api → Events**, a uno de los dos deploys anteriores (límite del plan gratuito). Vuelve el código, no el esquema.
- **Logs:** JSON en **Logs**. Cada petición lleva su `request_id`, que también va en la cabecera `X-Request-ID` de la respuesta.
- **Backups:** Neon gratuito permite restaurar a cualquier punto de las **últimas 6 horas** y guardar una instantánea manual. Haz una antes de cada migración delicada (**Branches → Create snapshot**).
- **Acceder a la base:** con la URL de Neon desde tu máquina (`psql "<url sin &pool_…>"`), o desde su *SQL Editor*. La imagen de la API no tiene shell, así que la *Shell* de Render tampoco está disponible en el plan gratuito.
- **Rotar `JWT_SECRET`:** cámbialo en **Environment** (*Generate*) y vuelve a desplegar. Cierra todas las sesiones.
- **Contraseña de Neon:** si se filtra, en Neon haz **Roles → Reset password** y actualiza `DATABASE_URL` en Render.

## Pasar a pago más adelante

Si el proyecto crece, este es el cambio:

1. **API en un plan de pago** (`plan: 0.5c-512mb` o mayor): siempre encendida, sin monitor.
2. **Migraciones en pre-deploy:** `preDeployCommand: /app/migrate` y quitar `MIGRATE_ON_START`. Se aplican en una instancia aparte, antes del build nuevo. La imagen ya lo soporta: `/app/migrate` se ejecuta directamente, sin shell.
3. **Base:** seguir en Neon (de pago) o pasar a Render Postgres en la red privada, añadiendo `databases:` al Blueprint y `fromDatabase` en `DATABASE_URL`.
4. **Varias instancias:** primero `EVENTS_BACKEND=postgres` (decisión 8 de `DECISIONS.md`), luego `numInstances`. Los límites de streams y de intentos son por instancia (`SECURITY.md`).
