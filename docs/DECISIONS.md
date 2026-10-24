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
| Crear, editar y borrar etiquetas de un board | ✓ | ✓ | |
| Gestionar boards y columnas (crear, renombrar, borrar, reordenar) | ✓ | | |
| Gestionar miembros y ajustes | ✓ | | |

Lo que no está en la matriz se deniega. Gestionar boards y columnas es solo de admin porque la definición original de `member` se limita a tareas; cambiarlo es una línea de la matriz.

- **Un solo punto de control:** toda ruta bajo un workspace pasa por el middleware `requireAccess`, que resuelve el workspace desde el path o a través del board, la columna o la tarea indicados, lee el rol del usuario y aplica la matriz. Los handlers no repiten comprobaciones.
- **403 sin revelar existencia:** si el usuario no es miembro, si el recurso no existe o si el id no es válido, la respuesta es la misma `403 "No tienes acceso a este workspace"`, idéntica byte a byte. Un miembro sin permiso recibe otro 403, "Tu rol no permite esta acción", porque a él no se le revela nada nuevo. Por eso los recursos inexistentes dan 403 y no 404.
- **Siempre al menos un admin:** cambiar roles y quitar miembros se hace en una transacción que bloquea la fila del workspace (`SELECT … FOR UPDATE`), vuelve a comprobar el rol de quien actúa (por si lo degradaron entre la autorización y la transacción) y cuenta admins antes de escribir. Si el cambio dejaría el workspace sin admin, responde `409 last_admin`. Así, dos admins que se degradan mutuamente a la vez no pueden dejarlo sin ninguno.

**Por qué:** centralizar la decisión en una matriz y un middleware hace que añadir rutas no pueda olvidar la autorización, y que los tests puedan recorrer la matriz completa (rol × acción y actor × ruta).

## 6. Campos de tarea, responsable y etiquetas

Las tareas tienen `priority` (`low|medium|high|urgent`, por defecto `medium`), `due_date` (fecha sin hora ni zona, `AAAA-MM-DD`), `assignee_id` y `archived_at`. Las etiquetas pertenecen a un board (`labels`, nombre único por board y color hexadecimal guardado en minúsculas) y se asignan con `task_labels`.

- **Todo es parte de la tarea:** prioridad, fecha, responsable, etiquetas y archivado se editan con el mismo `PATCH /tasks/{id}` y la misma guarda de `version` (decisión 1). Cambiar las etiquetas de una tarea sube su versión y un cliente desactualizado recibe el 409 con la tarea completa, etiquetas incluidas. `label_ids` reemplaza el conjunto entero en lugar de añadir o quitar, para que el resultado no dependa del orden de llegada.
- **El responsable es miembro del workspace:** se valida dentro de la transacción y la fila de membresía se bloquea con `FOR KEY SHARE` hasta el commit. Quitar a un miembro deja sin responsable sus tareas de ese workspace, en la misma transacción. Cada tarea sube su versión y registra `task_updated`. Sin el bloqueo, una asignación simultánea a la baja podría dejar asignado a alguien que ya no es miembro (hay un test que lo provoca). Un viewer puede ser responsable; lo que no puede es editar la tarea. Para evitar interbloqueos, el orden de bloqueo es siempre membresía → columna → etiquetas → tarea.
- **Etiquetas solo del mismo board:** al asignarlas se comprueba que todas existen y son del board de la tarea, y se bloquean con `FOR KEY SHARE` para que no se borren a medias. Como una tarea nunca cambia de board, siguen siendo válidas al moverla. Renombrar o borrar una etiqueta **no** cambia la versión de sus tareas: se edita la etiqueta, no la tarea.
- **Archivar** se hace con `"archived": true|false`, y la API guarda la hora en `archived_at`. Archivar una tarea que ya lo está conserva la fecha original. `GET /boards/{id}` omite las archivadas salvo con `?include_archived=true`. Siguen ocupando su posición, así que desarchivar las devuelve a su sitio.
- **Sin N+1 en el board:** `GET /boards/{id}` hace siempre cinco consultas en un snapshot `REPEATABLE READ`: el board, sus etiquetas, sus columnas, sus tareas (con un `LEFT JOIN` al nombre del responsable) y todas las parejas tarea–etiqueta. Luego se agrupan en memoria. Un test cuenta las consultas con un tracer de pgx y comprueba que son las mismas con 1 tarea que con 31.
- **Actividad con valor anterior y nuevo:** `task_updated` guarda en `changes` solo los campos que cambiaron, como `{"from": …, "to": …}`, con los valores codificados igual que en la API: fechas `AAAA-MM-DD`, ids, listas de ids ordenadas y `null` para vacío. `task_created` incluye los campos iniciales.
- **Permisos:** gestionar etiquetas es una acción propia (`manage_labels`) para admin y member, porque etiquetar forma parte de trabajar con tareas y no de la estructura del board. Leerlas está permitido a cualquier miembro.
- Los campos nuevos de la API van en `snake_case` (`due_date`, `assignee_id`, `label_ids`), igual que las respuestas. El cuerpo de `/tasks/{id}/move` sigue en camelCase (`columnId`, …) tal como se definió.

**Por qué:** mantener una sola guarda de versión para todo lo editable evita estados mezclados entre dos clientes, y validar con bloqueos dentro de la transacción (en lugar de comprobar antes) cierra las carreras con bajas de miembros y borrado de etiquetas sin bloquear tablas enteras.
