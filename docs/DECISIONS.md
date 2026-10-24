# Decisiones de arquitectura

## 1. Concurrencia optimista con columna `version` en `tasks`

Cada fila de `tasks` tiene una columna entera `version`. Toda actualización debe enviar la versión que el cliente leyó, y el `UPDATE` solo se aplica si coincide (`WHERE id = $1 AND version = $2`), incrementándola en el mismo paso.

Si no coincide, la API responde **409 Conflict** con el estado actual de la tarea en el cuerpo, para que el cliente pueda reconciliar sin otra petición.

**Por qué:** evita sobrescrituras silenciosas entre usuarios simultáneos sin necesidad de bloqueos.

## 2. Orden de tarjetas con `position` fraccionaria

El orden dentro de una columna se guarda en un campo `position` numérico fraccionario. Al mover una tarjeta entre A y B, su nueva posición es el punto medio `(A + B) / 2`; al inicio o al final se resta o suma un paso fijo.

**Por qué:** mover una tarjeta modifica una sola fila en lugar de renumerar toda la columna. Si la precisión se agota tras muchas inserciones en el mismo hueco, se rebalancea la columna.

## 3. SSE para tiempo real

Los cambios se envían a los clientes mediante **Server-Sent Events** (`text/event-stream`). Las escrituras siguen yendo por la API REST normal.

**Por qué:** el flujo en tiempo real es solo servidor → cliente; SSE funciona sobre HTTP estándar, reconecta automáticamente y es más simple que WebSockets.

## 4. Autenticación: JWT de acceso corto y refresh token opaco en cookie

- **Contraseñas** con **argon2id** (parámetros mínimos de OWASP: 19 MiB, 2 iteraciones, 1 hilo) en formato PHC (`$argon2id$v=19$m=…,t=…,p=…$sal$hash`). Los parámetros viajan en el hash, así que se pueden endurecer sin invalidar los existentes. La contraseña debe tener entre 8 y 128 caracteres; el máximo acota el coste de hashear por petición.
- **Access token:** JWT HS256 de 15 minutos con `sub` = id de usuario e `iss` = `tablero`. Al verificar se exige HS256 (rechaza `alg: none` y otros algoritmos), el emisor y la expiración. El cliente lo envía en `Authorization: Bearer`.
- **Refresh token:** valor aleatorio de 32 bytes en la cookie `refresh_token` (`HttpOnly; Secure; SameSite=Lax; Path=/auth`, 30 días). En la base solo se guarda su **SHA-256** (tabla `refresh_tokens`), de modo que una fuga de la base no da sesiones válidas. `Path=/auth` evita que viaje en el resto de peticiones.
- **Login sin pistas:** cualquier fallo (correo inexistente, contraseña incorrecta, campos vacíos) responde el mismo `401` con el mismo cuerpo, "Correo o contraseña incorrectos". Si el correo no existe se verifica igualmente un hash ficticio, para que el tiempo de respuesta no delate qué cuentas existen.
- **Pendiente:** `/auth/refresh` (rotación del refresh token) y `/auth/logout` (revocación); la tabla ya tiene `revoked_at` para ello.

**Por qué:** un access token corto sin estado evita consultar la base en cada petición, y el refresh token largo queda fuera del alcance de JavaScript (`HttpOnly`) y es revocable porque vive en la base. El registro sí revela si un correo ya existe (409); evitarlo exigiría un flujo con confirmación por correo.

## 5. Roles y autorización por workspace

Los permisos se definen por **rol dentro de cada workspace** (`admin`, `member`, `viewer`) en una única matriz (`internal/auth/permissions.go`):

| Acción | admin | member | viewer |
|---|---|---|---|
| Leer workspace, miembros, boards, tareas y actividad | ✓ | ✓ | ✓ |
| Crear, editar, mover y borrar tareas | ✓ | ✓ | |
| Gestionar boards y columnas (crear, renombrar, borrar, reordenar) | ✓ | | |
| Gestionar miembros y ajustes | ✓ | | |

Lo que no está en la matriz se deniega. Gestionar boards y columnas es solo de admin porque la definición original de `member` se limita a tareas; cambiarlo es una línea de la matriz.

- **Un solo punto de control:** toda ruta bajo un workspace pasa por el middleware `requireAccess`, que resuelve el workspace desde el path o a través del board, la columna o la tarea indicados, lee el rol del usuario y aplica la matriz. Los handlers no repiten comprobaciones.
- **403 sin revelar existencia:** si el usuario no es miembro, si el recurso no existe o si el id no es válido, la respuesta es la misma `403 "No tienes acceso a este workspace"`, idéntica byte a byte. Un miembro sin permiso recibe otro 403, "Tu rol no permite esta acción", porque a él no se le revela nada nuevo. Por eso los recursos inexistentes dan 403 y no 404.
- **Siempre al menos un admin:** cambiar roles y quitar miembros se hace en una transacción que bloquea la fila del workspace (`SELECT … FOR UPDATE`), vuelve a comprobar el rol de quien actúa (por si lo degradaron entre la autorización y la transacción) y cuenta admins antes de escribir. Si el cambio dejaría el workspace sin admin, responde `409 last_admin`. Así, dos admins que se degradan mutuamente a la vez no pueden dejarlo sin ninguno.

**Por qué:** centralizar la decisión en una matriz y un middleware hace que añadir rutas no pueda olvidar la autorización, y que los tests puedan recorrer la matriz completa (rol × acción y actor × ruta).
