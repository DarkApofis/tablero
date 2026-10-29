# Seguridad

Modelo de amenazas breve de Tablero: qué se protege, frente a quién, cómo y qué queda abierto. Las decisiones de diseño están en [`DECISIONS.md`](DECISIONS.md); la autenticación en la decisión 4 y CSRF en la 10.

## Qué se protege

- **Cuentas:** contraseñas (hash argon2id), refresh tokens (solo su SHA-256 en la base) y access tokens (JWT de 15 min).
- **Datos de los workspaces:** tableros, tareas y actividad. Solo los miembros los leen, y escribir depende del rol (decisión 5).
- **Disponibilidad** de la API y de sus streams en vivo.

## Frente a quién

| Actor | Puede | Ejemplos |
|---|---|---|
| Visitante anónimo | Llamar a la API pública | Adivinar contraseñas, registrar cuentas en masa, agotar recursos |
| Sitio malicioso abierto en el navegador de la víctima | Disparar peticiones con sus cookies | CSRF, login forzado, enmarcar la app (*clickjacking*) |
| Usuario autenticado | Llamar a la API con su token | Leer o cambiar workspaces ajenos, abrir cientos de streams |
| Atacante en la red | Observar tráfico sin TLS | Robar cookies o tokens |
| Quien filtre la base | Leer las tablas | Usar sesiones o contraseñas filtradas |

**Fronteras de confianza:**
- **Navegador ↔ frontend** (Next.js): sirve páginas y nunca ve tokens.
- **Navegador ↔ API:** la única que decide sobre los datos.
- **API ↔ Postgres:** red interna.
- **Proxy inverso:** la API solo se fía de `X-Forwarded-For` y `X-Forwarded-Proto` con `TRUST_PROXY=true`.

## Amenazas y defensas

| Amenaza | Defensa | Dónde |
|---|---|---|
| Fuerza bruta y *credential stuffing* en el login | Por IP: 20 intentos y luego 1 cada 3 s. Por cuenta: 10 fallos y luego 1 cada 90 s, contando solo fallos para que el dueño siga pudiendo entrar. `429` con `Retry-After`. Las IPv6 cuentan por /64 | `httpapi.DefaultAuthLimits` |
| Registro o invitados en masa | Registro: 10/h por IP y 3/h por correo. Invitados: 5/h por IP y `DEMO_MAX_GUESTS` en total | `ratelimit.go`, `internal/demo` |
| Enumeración de cuentas por el login | Mismo `401` y mismo tiempo (hash ficticio) si el correo no existe | decisión 4 |
| Robo de tokens por XSS | Access token solo en memoria; refresh token en cookie `HttpOnly` con `Path=/auth`; CSP en el frontend; React escapa todo lo que pinta y no hay `dangerouslySetInnerHTML` | decisión 4, `next.config.ts` |
| Refresh token robado | Rotación en cada uso; si vuelve uno rotado hace más de 30 s, se revocan todas las sesiones del usuario | decisión 4 |
| CSRF y login forzado | `SameSite`, comprobación de `Origin`/`Sec-Fetch-Site` en toda escritura y JSON obligatorio (`415` si no) | decisión 10, `security.go` |
| Lectura desde otro origen | CORS solo para `CORS_ORIGINS`, que se valida al arrancar (sin comodines) | `withCORS` |
| *Clickjacking* | `frame-ancestors 'none'` y `X-Frame-Options: DENY` en la API y el frontend | |
| *Sniffing* y fugas por *referrer* | `nosniff`; `Referrer-Policy: no-referrer` (API) y `strict-origin-when-cross-origin` (frontend) | |
| Tráfico sin cifrar | Cookies `Secure` (`COOKIE_SECURE=true` por defecto); HSTS cuando la petición llega por HTTPS | `withSecurityHeaders`, `next.config.ts` |
| Escalada entre workspaces | Toda ruta se autoriza con el rol del usuario en el workspace del recurso. Un recurso ajeno, uno inexistente y un id mal formado dan el mismo `403` | decisión 5 |
| Boards privados vistos por quien no debe | La visibilidad se comprueba en el mismo `requireAccess` que el rol, para todas las rutas del board (también el stream, en cada heartbeat). Para quien no lo tiene, el board es indistinguible de uno inexistente | decisión 5 |
| Adivinar enlaces de invitación | Token aleatorio de 128 bits (`crypto/rand`), caduca a los 7 días y se invalida al crear otro | `workspace.Invitations` |
| Inyección SQL | Todas las consultas son de sqlc, con parámetros. Las únicas sentencias armadas a mano (`LISTEN`, y `CREATE`/`DROP DATABASE` en tests) llevan identificadores constantes o generados, citados con `pgx.Identifier` | `queries/`, `eventbus.go`, `pgtest` |
| Cuerpos enormes o clientes lentos (*slowloris*) | Body ≤ 256 KiB (`413`); cabeceras ≤ 32 KiB y en 5 s; petición completa en 15 s; escrituras con plazo de 30 s (10 s por mensaje en los streams) | `withBodyLimit`, `cmd/api/main.go` |
| Agotar conexiones con streams | `SSE_MAX_PER_USER` (10) y `SSE_MAX_PER_BOARD` (1000) por instancia (`429 too_many_streams`); clientes lentos expulsados (decisión 9) | `streamCounter` |
| Filtración de la base | Solo hashes: argon2id para contraseñas y SHA-256 para refresh tokens | decisión 4 |
| Configuración insegura | La API no arranca con `JWT_SECRET` corto, orígenes mal formados o `SameSite=None` sin `Secure`; lista todos los problemas | `internal/config` |

## Riesgos aceptados y pendientes

- **CSP con `'unsafe-inline'` en `script-src`.** Next.js incrusta scripts para hidratar las páginas generadas en el build. Usar *nonces* obligaría a renderizar cada página en cada petición y perder el prerenderizado. Se compensa sin código de terceros, con React escapando y con `connect-src` limitado a la API, lo que impide sacar datos a otro sitio. `default-src`, `object-src`, `base-uri`, `form-action` y `frame-ancestors` son estrictos.
- **Límites en memoria, por instancia.** Con N instancias tras un balanceador, los límites efectivos se multiplican por N. Si se escala, el siguiente paso es un almacén compartido (Postgres o Redis).
- **Bloqueo de una cuenta por terceros.** Quien conozca un correo puede gastar sus 10 fallos, y su dueño queda a un intento cada 90 s mientras dure el ataque. Es el precio del límite por cuenta.
- **API pública detrás del proxy de Vercel** (despliegue gratuito, `docs/DEPLOY.md`). Quien llame a la URL de Render directamente puede elegir, con `X-Forwarded-For`, la IP que le atribuye el límite por IP. Los límites por correo, las cookies y la protección CSRF no dependen de ella.
- **`TRUST_PROXY` mal puesto.** Activado sin proxy delante, cualquiera falsifica su IP con `X-Forwarded-For` y esquiva el límite por IP. Desactivado detrás de un proxy, todos los clientes comparten la IP del proxy.
- **`/metrics` sin autenticación:** debe quedar en la red interna (decisión 9).
- **El modo demo publica credenciales,** también la del admin. No debe activarse sobre una base con datos reales (README, «Modo demo»).
- **Sin verificación de correo ni 2FA.** El registro revela si un correo ya existe (`409`). Por la misma razón, quien registre primero un correo invitado entra al workspace con esa invitación; y un admin que invita sabe, por la respuesta, si ese correo ya tenía cuenta.
- **Los enlaces de invitación se guardan en claro,** para que un admin pueda volver a copiarlos. Quien lea la base podría usarlos; pero quien lee la base ya tiene acceso a todo lo demás.
- **Las direcciones de workspace son públicas a medias:** cualquier usuario con sesión puede saber si una dirección está ocupada (`GET /workspace-slugs?slug=`), no a qué workspace corresponde.
- **Purga de `refresh_tokens` caducados:** pendiente.
- **`golang.org/x/crypto`** arrastra un aviso (GO-2026-5932) sobre `openpgp`, que el código no usa y que no tiene corrección.

## Comprobaciones automáticas

| Herramienta | Qué mira | Cuándo |
|---|---|---|
| `govulncheck ./...` | Vulnerabilidades conocidas alcanzables desde el código, incluida la biblioteca estándar | CI |
| gosec (dentro de `golangci-lint`) | Patrones inseguros. Las excepciones se marcan en el código con `#nosec <regla> -- motivo` | CI |
| `pnpm audit --prod` | Dependencias del frontend con avisos | CI |
| Tests | Rate limiting, CSRF, cabeceras, cookies, tamaño de body, límites de streams (`security_test.go`); CSP sin violaciones en un navegador real (`e2e/security.spec.ts`) | CI |

Para correrlas a mano:

```sh
cd backend
go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...
golangci-lint run ./...                  # incluye gosec
cd ../frontend && pnpm audit --prod
```

**Última revisión** (octubre de 2026):
- govulncheck encontró 24 vulnerabilidades alcanzables: 23 de la biblioteca estándar de Go 1.26.2 y una de `moby/go-archive` v0.2.0, que solo llega por testcontainers. Se corrigieron subiendo a **Go 1.26.10** y a **go-archive v0.3.0**.
- gosec dio 17 avisos:
  - Corregidos: conversiones de enteros sin acotar, un healthcheck que aceptaba cualquier URL (ahora solo `localhost`) y atributos de cookie.
  - Marcados como falsos positivos: nombres de constantes que «parecen» credenciales, las credenciales públicas del demo y el *jitter* con `math/rand`.
- `pnpm audit`: sin avisos.

## Configuración de seguridad

| Variable | Por defecto | |
|---|---|---|
| `CORS_ORIGINS` | `http://localhost:3000` | Únicos orígenes que pueden llamar a la API desde un navegador (CORS y CSRF) |
| `COOKIE_SECURE` | `true` | `Secure` en las cookies de sesión |
| `COOKIE_SAMESITE` | `lax` | `lax`, `strict` o `none` (este, solo con `COOKIE_SECURE=true`, para un frontend en otro sitio) |
| `COOKIE_DOMAIN` | — | `Domain` de las cookies; sin él, solo el host de la API |
| `TRUST_PROXY` | `false` | Tomar IP y esquema de `X-Forwarded-For` / `X-Forwarded-Proto`; solo detrás de un proxy que los fije |
| `TRUST_PROXY_HOPS` | `1` | Proxies que añaden a `X-Forwarded-For`; la IP del cliente es la entrada que está a esa distancia del final |
| `COOKIE_PATH_PREFIX` | — | Prefijo de la ruta de las cookies (`/api` cuando el frontend sirve la API en su origen) |
| `RATE_LIMIT` | `true` | Límites de login, registro e invitados |
| `SSE_MAX_PER_USER` / `SSE_MAX_PER_BOARD` | `10` / `1000` | Streams abiertos por instancia |

## Reportar un problema

Escribe a quien mantenga el repositorio antes de abrir un issue público, con los pasos para reproducirlo.
