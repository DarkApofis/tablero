# Despliegue gratuito: Vercel + Render + Neon

Todo con planes gratuitos:

- **Frontend** (Next.js) en **Vercel** (Hobby), en `https://tablero.vercel.app`, o la URL que Vercel asigne si ese nombre está ocupado.
- **API** (Go, Docker) en **Render**, en una instancia gratuita en Frankfurt, declarada en [`render.yaml`](../render.yaml), un *Blueprint* de Render.
- **Base de datos:** **Neon**, Postgres administrado gratuito en Frankfurt (proyecto `noisy-dawn-97517345`, Postgres 18).

```
navegador ──HTTPS──▶ tablero.vercel.app
                       ├─ /          páginas (Next.js en Vercel)
                       └─ /api/*  ──▶ tablero-api-XXXX.onrender.com/*   (proxy de Vercel)
                                          │ TLS
                                          ▼
                                     Neon (Postgres 18, AWS Frankfurt)

monitor externo ──cada 10 min──▶ tablero-api-XXXX.onrender.com/health
```

## Por qué la API se sirve bajo `/api` del frontend

`*.vercel.app` y `*.onrender.com` son **sitios distintos** para el navegador. Si el frontend llamara directamente a Render, las cookies de la API (la de sesión y la del stream en vivo, que `EventSource` necesita porque no puede enviar cabeceras) serían cookies de terceros. Safari las bloquea siempre, y Chrome si el usuario lo tiene activado: la sesión se perdería al recargar y el tablero no se actualizaría en vivo.

Por eso Vercel reenvía `/api/*` a Render (`rewrites` en `frontend/next.config.ts`, activado por `API_PROXY_TARGET`). El navegador solo habla con `tablero.vercel.app`, y las cookies son propias, con `Path=/api/auth` y `Path=/api/boards` (`COOKIE_PATH_PREFIX`).

- **El stream SSE sobrevive al proxy.** Vercel mantiene la conexión mientras la API envíe algo al menos cada 120 s ([changelog de Vercel](https://vercel.com/changelog/cdn-origin-timeout-increased-to-two-minutes)), y la API manda un `ping` cada 25 s.
- **Nadie lo retiene en un búfer.** La API marca el stream con `Cache-Control: no-transform` para que ningún proxy lo comprima: comprimido, los eventos se quedarían en el búfer del compresor. El proxy de Next lo hacía en las pruebas locales.

## Qué implica el plan gratuito

| | Gratis (esta configuración) | De pago (al final de esta guía) |
|---|---|---|
| Migraciones | La API las aplica al arrancar, antes de abrir el puerto (`MIGRATE_ON_START`) | Paso de pre-deploy aparte (`preDeployCommand`) |
| Dormirse | Tras 15 min sin peticiones, salvo que un monitor la mantenga despierta | Siempre encendida |
| Base | Neon: 1 GB, se suspende tras 5 min sin uso, restauración de las últimas 6 h | Más espacio, copias, siempre activa |
| CPU de la API | 0,1 CPU: un login (argon2id) tarda bastante más que en local | 0,5 CPU o más |
| Horas | 750 h al mes por workspace de Render. Este servicio despierto 24/7 gasta ~744 h | Sin límite |

Fuentes: [Render, plan gratuito](https://render.com/docs/free), [Neon, precios](https://neon.com/pricing), [Vercel como proxy](https://vercel.com/kb/guide/vercel-reverse-proxy-rewrites-external).

## Qué hace cada pieza del despliegue

1. **Build:** Render construye `backend/Dockerfile`. Solo hace falta un deploy cuando cambia `backend/**` o `render.yaml` (`buildFilter`). Vercel construye `frontend/` en cada push.
2. **Arranque de la nueva versión de la API:**
   - valida la configuración;
   - aplica las migraciones pendientes;
   - se conecta a la base;
   - y solo entonces abre el puerto.
   
   Si algo falla, el proceso termina, el health check nunca pasa y el deploy se da por fallido.
3. **Health check:** Render pasa el tráfico a la versión nueva cuando `GET /health` responde 200.
4. **Apagado:** la versión anterior recibe SIGTERM. Pone `/ready` en 503, sigue atendiendo 5 s y da hasta 20 s a las peticiones en curso. Los streams se cierran, y los navegadores reconectan solos y recuperan lo perdido con `Last-Event-ID`.
5. **Auto-deploy:** Render despliega cada commit a `main` cuando el CI pasa (`autoDeployTrigger: checksPass`).

## Variables de entorno y secretos

### API (Render)

| Variable | Valor | Tipo | Para qué |
|---|---|---|---|
| `DATABASE_URL` | URL **directa** de Neon + `&pool_max_conn_idle_time=4m` (en tu `.env.neon`: `RENDER_DATABASE_URL`) | **Secreto**, lo pones tú (`sync: false`) | Conexión a Postgres. Directa porque las migraciones usan un advisory lock de sesión, que el pooler de Neon no mantiene. El parámetro cierra las conexiones ociosas para que Neon pueda suspenderse |
| `JWT_SECRET` | 256 bits aleatorios | **Secreto**, lo genera Render una vez (`generateValue`) | Firma de los access tokens. Rotarlo cierra todas las sesiones |
| `CORS_ORIGINS` | `https://tablero.vercel.app`, o la URL real de Vercel | Lo pones tú (`sync: false`) | Origen del frontend. La comprobación CSRF exige que el `Origin` de toda escritura coincida |
| `COOKIE_PATH_PREFIX` | `/api` | Fijo | Las cookies van a `/api/auth` y `/api/boards`, las rutas que ve el navegador |
| `COOKIE_SECURE` / `COOKIE_SAMESITE` | `true` / `lax` | Fijo | Cookies solo por HTTPS y propias del sitio de Vercel |
| `TRUST_PROXY` / `TRUST_PROXY_HOPS` | `true` / `2` | Fijo | La IP del cliente viene en `X-Forwarded-For` a través de dos proxies (Vercel y Render). Se verifica en el paso 7 |
| `MIGRATE_ON_START` / `MIGRATE_LOCK_TIMEOUT` | `true` / `5s` | Fijo | Migrar al arrancar (el plan gratuito no tiene pre-deploy) y cuánto puede esperar una migración por un bloqueo de tabla |
| `PORT` | `8080` | Fijo | Puerto dentro del contenedor |
| `EVENTS_BACKEND` | `memory` | Fijo | Eventos en vivo en memoria: una sola instancia |
| `SHUTDOWN_DELAY` / `SHUTDOWN_TIMEOUT` | `5s` / `20s` | Fijo | Apagado ordenado |
| `RATE_LIMIT` | `true` | Fijo | Límites de login, registro e invitados |
| `DEMO_MODE` | `false` | Fijo | Producción: sin invitados ni reset de datos |
| `GOMEMLIMIT` | `400MiB` | Fijo | El recolector de Go se ajusta antes de llegar a los 512 MB |

### Frontend (Vercel)

| Variable | Valor | Para qué |
|---|---|---|
| `NEXT_PUBLIC_API_URL` | `/api` | El cliente llama a la API en su propio origen. Se incrusta en el build |
| `API_PROXY_TARGET` | `https://tablero-api-XXXX.onrender.com` (la URL de Render, sin barra final) | Adónde reenvía Vercel `/api/*`. Solo la ve el servidor |
| `ENABLE_EXPERIMENTAL_COREPACK` | `1` | Vercel usa la versión de pnpm de `package.json` (`pnpm@11.9.0`) |

Los «fijos» de la API viven en `render.yaml` y se cambian con un commit. Los secretos no están en el repositorio: Render y Vercel los guardan en sus paneles. Ninguna variable de Vercel es secreta.

## Migraciones: se aplican antes y son compatibles hacia atrás

La versión nueva de la API aplica las migraciones **antes** de aceptar tráfico, pero la versión anterior **sigue sirviendo** con el esquema nuevo hasta el relevo. Además, un *rollback* devuelve el código anterior, pero no revierte la base. Por eso toda migración tiene que funcionar con la versión de la API que ya está en producción.

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

**Versiones de Postgres:** Neon usa la 18; Compose y el CI, la 16. Las 4 migraciones actuales y la API se probaron contra Neon (18) de punta a punta, pero el CI no prueba la 18. Conviene pasar el CI a 18.

## Health check, SSE y que la instancia no se duerma

- **Health check:** `/health` (liveness). No consulta la base, por dos motivos: una caída breve de Neon no debe provocar reinicios, y el monitor no debe despertar a Neon cada 10 minutos. La API no abre el puerto sin haber alcanzado la base.
- **No dormirse:**
  - Render duerme una instancia gratuita tras 15 minutos sin tráfico entrante. Cuenta las peticiones HTTP y los mensajes de WebSocket, pero no dice nada de SSE. Un stream abierto, en el que el cliente no envía nada, probablemente no cuente.
  - Por eso un monitor externo gratuito llama a `/health` cada 10 minutos (paso 5). Sin él, el siguiente visitante esperaría alrededor de un minuto y los streams abiertos se cortarían; al despertar se reconectan solos.
  - Render no prohíbe ni documenta esta práctica.
- **Neon sí se duerme**, y está bien:
  - Se suspende tras 5 minutos sin consultas, y la primera consulta después tarda algo más.
  - Mientras alguien tiene un tablero abierto, el stream comprueba sus permisos cada 25 s y Neon sigue despierto.
  - Sin nadie conectado, la API cierra sus conexiones ociosas a los 4 minutos y Neon se suspende.
- **Una instancia:** el plan gratuito no escala, y `EVENTS_BACKEND=memory` asume un solo proceso.

## Paso a paso

Los clics y los comandos los haces tú. Para en cada paso y confirma antes de seguir.

### 1. Repositorio en GitHub ✓

`DarkApofis/tablero`, con el CI en verde.

### 2. Base de datos en Neon ✓

Proyecto `noisy-dawn-97517345` (Frankfurt, Postgres 18), enlazado con la CLI (`neon.ts`, `package.json`; `.neon` está ignorado). Las 4 migraciones ya están aplicadas. Las URLs están en tu `.env.neon`, ignorado por git. Para volver a traerlas:

```sh
npm i -g neon@latest && neon login
neon env pull        # DATABASE_URL (pooler), DATABASE_URL_UNPOOLED (directa), NEON_BRANCH
```

Ojo: escribe en `.env` si existe, y ese archivo es tu configuración de desarrollo local. Lleva esas variables a `.env.neon` y deja `.env` como estaba.

### 3. API en Render, desde el Blueprint

En [dashboard.render.com](https://dashboard.render.com): **New → Blueprint**, conecta GitHub y elige `DarkApofis/tablero`, rama `main`. Render lee `render.yaml` y muestra **tablero-api** con el plan **Free**. Te pedirá:

- **`DATABASE_URL`:** el valor de `RENDER_DATABASE_URL` de tu `.env.neon`.
- **`CORS_ORIGINS`:** `https://tablero.vercel.app`. Si en el paso 6 Vercel te da otra URL, lo corriges entonces.

Pulsa **Apply**.

### 4. Comprobar el primer deploy

En **tablero-api → Logs**:

```
"msg":"migrations up to date","applied":0     ← ya estaban aplicadas en Neon
"msg":"database connected"
"msg":"server starting","addr":":8080"
```

Con la URL que te da Render (`https://tablero-api-XXXX.onrender.com`):

```sh
curl -s https://tablero-api-XXXX.onrender.com/health   # {"status":"ok"}
curl -s https://tablero-api-XXXX.onrender.com/ready    # {"status":"ok"}: llega a Neon
```

### 5. Monitor para que la API no se duerma

En [UptimeRobot](https://uptimerobot.com) (o [cron-job.org](https://cron-job.org)), crea un monitor gratuito:

- **Tipo:** HTTP(s)
- **URL:** `https://tablero-api-XXXX.onrender.com/health`. Directo a Render, no por Vercel, para no gastar peticiones de Vercel.
- **Intervalo:** 10 minutos
- **Alerta:** tu correo.

### 6. Frontend en Vercel

1. En [vercel.com](https://vercel.com): **Add New → Project** e importa `DarkApofis/tablero`.
2. **Project Name:** `tablero`. **Root Directory:** `frontend`. Vercel detecta Next.js y pnpm.
3. En **Environment Variables**, añade:
   - `NEXT_PUBLIC_API_URL` = `/api`
   - `API_PROXY_TARGET` = `https://tablero-api-XXXX.onrender.com`, tu URL de Render, sin barra final.
   - `ENABLE_EXPERIMENTAL_COREPACK` = `1`
4. **Deploy.** Al terminar, apunta la URL de producción (`https://tablero.vercel.app` o similar). Si no es `https://tablero.vercel.app`, cambia `CORS_ORIGINS` en **Render → tablero-api → Environment** a esa URL exacta. Render redespliega solo.

### 7. Verificar de punta a punta

1. Abre la URL de Vercel, **regístrate**, crea un workspace y un tablero. **Recarga la página:** la sesión debe seguir abierta. Eso confirma que la cookie de sesión funciona a través del proxy.
2. Abre el mismo tablero en otra ventana (o en el móvil) y mueve una tarjeta: debe moverse en la otra sin recargar. Eso confirma el SSE a través del proxy.
3. Comprobaciones con `curl`:

   ```sh
   APP=https://tablero.vercel.app        # tu URL de Vercel
   curl -s $APP/api/ready                                       # {"status":"ok"}: la API vía proxy
   curl -sI $APP/login | grep -iE "^HTTP|content-security"      # cabeceras del frontend
   curl -s -X POST $APP/api/auth/logout -H "Origin: https://evil.example"   # 403 csrf_rejected
   ```

4. **IP del cliente (`TRUST_PROXY_HOPS`).** El límite de intentos de login es por IP. Hay que comprobar que la API ve la IP real a través de Vercel y Render:

   ```sh
   # 25 logins fallidos desde tu red: a partir del ~21 deben dar 429.
   for i in $(seq 1 25); do curl -s -o /dev/null -w "%{http_code} " -X POST $APP/api/auth/login \
     -H "Content-Type: application/json" -d "{\"email\":\"prueba$i@example.com\",\"password\":\"x\"}"; done; echo
   ```

   Después, desde otra red (los datos del móvil), un login fallido tiene que dar **401**, no 429. Si da 429, todos los clientes comparten una IP de proxy: hay que ajustar `TRUST_PROXY_HOPS`.

## Operación

- **Desplegar:** haz merge a `main`. Cuando el CI pasa, Render construye la API, que migra, arranca y releva a la anterior. Vercel despliega el frontend.
- **Rollback de la API:** en **tablero-api → Events**, a uno de los dos deploys anteriores (límite del plan gratuito). Vuelve el código, no el esquema. El frontend tiene su propio *Instant Rollback* en Vercel.
- **Logs:** JSON en **Render → Logs**. Cada petición lleva su `request_id`, que también va en la cabecera `X-Request-ID` de la respuesta.
- **Backups:** Neon gratuito permite restaurar a cualquier punto de las **últimas 6 horas** y guardar una instantánea manual. Haz una antes de cada migración delicada.
- **Acceder a la base:** con la URL directa de Neon desde tu máquina (`psql "<DATABASE_URL_UNPOOLED>"`), o desde su *SQL Editor*.
- **Rotar `JWT_SECRET`:** cámbialo en **Render → Environment** (*Generate*). Cierra todas las sesiones.
- **Contraseña de Neon:** si se filtra, en Neon haz **Roles → Reset password**, vuelve a traer las URLs con `neon env pull` y actualiza `DATABASE_URL` en Render.

## Riesgo conocido de esta configuración

La URL de Render (`*.onrender.com`) sigue siendo pública. Quien llame a la API directamente, saltándose Vercel, puede poner lo que quiera en `X-Forwarded-For` y, con `TRUST_PROXY_HOPS=2`, elegir la IP que el límite por IP le atribuye. Los límites por correo siguen aplicando, y las cookies y la protección CSRF no dependen de la IP. Un dominio propio delante de las dos piezas, o un plan de pago con red privada, lo cerrarían.

## Pasar a pago más adelante

1. **API en un plan de pago** (`plan: 0.5c-512mb` o mayor): siempre encendida, sin monitor.
2. **Migraciones en pre-deploy:** `preDeployCommand: /app/migrate` y quitar `MIGRATE_ON_START`. La imagen ya lo soporta: `/app/migrate` se ejecuta directamente, sin shell.
3. **Dominio propio:** `app.<dominio>` en Vercel y `api.<dominio>` en Render. Las cookies serían del mismo sitio sin proxy (`NEXT_PUBLIC_API_URL=https://api.<dominio>`, sin `COOKIE_PATH_PREFIX`), y `TRUST_PROXY_HOPS` volvería a 1.
4. **Varias instancias:** primero `EVENTS_BACKEND=postgres` (decisión 8 de `DECISIONS.md`), luego `numInstances`. Los límites de streams y de intentos son por instancia (`SECURITY.md`).
