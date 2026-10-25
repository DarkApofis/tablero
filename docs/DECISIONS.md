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

Cómo está hecho (`GET /boards/{id}/events`):

- **Hub en memoria** (`adapters/events.Hub`): implementa el puerto `EventPublisher` de los casos de uso y el puerto `EventSubscriber` de `board.StreamService`. Guarda suscriptores por board, cada uno con un canal con buffer (64 eventos). **Publicar nunca bloquea:** si el buffer de un suscriptor está lleno (cliente lento), el hub lo da de baja y cierra su canal. El handler termina el stream, y `EventSource` reconecta y recupera lo perdido con `Last-Event-ID`. Un cliente lento no frena las escrituras ni a los demás clientes.
- **Eventos de dominio:** `task.created`, `task.updated`, `task.archived`, `task.moved`, `task.deleted`, `column.created`, `column.updated`, `column.reordered`, `column.deleted` y `label.changed`.
  - Cada cambio de contenido de un board escribe una entrada en `activity_log`, en la misma transacción, y **esa entrada es el evento**: `id` SSE = id de la fila, `event` = tipo, `data` = actor, la entidad completa (al crear) o los campos cambiados, y la `version` resultante de la tarea.
  - Los casos de uso publican solo tras el commit.
  - En la tabla se conservan los nombres de tipo de antes (`task_created`…), así que `/activity` no cambia de forma. El nombre con punto es el del evento, y la correspondencia está en `domain`.
  - `task.archived` es una edición que archiva. Al desarchivar se envía `task.updated`.
  - Si un movimiento obliga a reequilibrar, `task.moved` y `column.reordered` incluyen `rebalanced` con las nuevas posiciones, porque las demás filas cambian de posición sin ser editadas.
- **El log es la fuente; el hub solo despierta:** cada stream recuerda el id de lo último que envió y, al despertar, lee del log todo lo posterior. No hay huecos ni duplicados aunque dos `Publish` lleguen desordenados.
- **Orden de commit = orden de id, por board:** sin esto, dos transacciones del mismo board podrían tomar los ids 10 y 11 y confirmar 11 antes que 10, y un cliente que ya vio 11 nunca recibiría 10.
  - `activity.Append` toma `pg_advisory_xact_lock(board)` justo antes de insertar y lo mantiene hasta el commit. Las escrituras de un board se serializan solo en ese último paso.
  - Para que no haya interbloqueos, todo escritor lo toma **al final** y después solo inserta y lee. Por eso `LockBoard` pasó a `FOR NO KEY UPDATE`, compatible con el `KEY SHARE` de la FK de `activity_log`. Por la misma razón, el desasignado al quitar un miembro bloquea primero las tareas y luego los boards en orden.
  - Hay un test con 40 escrituras concurrentes que recibe los ids en el mismo orden que el log. Sin el lock pierde eventos.
- **Reconexión:** el stream se suscribe **antes** de leer el log, y luego reenvía lo posterior a `Last-Event-ID` (o `?last_event_id=` para un `EventSource` nuevo) antes de pasar al vivo.
  - Si faltan más de 500 eventos, o el id es mayor que el último del board, envía `event: resync` con `id` = el último evento: el cliente recarga el board y sigue desde ahí.
  - Sin `Last-Event-ID`, el stream empieza en vivo.
- **Presencia, derivada del stream y sin tabla:** el hub cuenta, por board y usuario, las conexiones abiertas. Varias pestañas son una presencia.
  - `presence.joined` sale cuando se abre la primera conexión. `presence.left` sale cuando se cierra la última **y pasan 4 s** sin que vuelva. Si reconecta antes (una recarga de página), no se envía nada; durante esos segundos sigue contando como presente.
  - Suscribirse y unirse a la presencia es un solo paso bajo el mutex del hub. Así el `presence.snapshot` de una conexión nueva cuadra con los eventos que llegan después, y esa conexión no recibe su propio `joined`.
  - Los mensajes de presencia van **sin `id` SSE**: según el estándar, el navegador conserva entonces el último `Last-Event-ID`, así que no interfieren con el reenvío desde `activity_log`. Tampoco se reenvían, porque cada conexión recibe el estado completo en su snapshot.
  - `GET /boards/{id}/presence` devuelve lo mismo sin abrir el stream.
  - Con varias instancias, la presencia es la unión de lo que anuncia cada una (decisión 8).
- **Excluir los propios eventos** solo si se pide: `?exclude_self=true` o `X-Exclude-Self: true`.
- Antes de todo se envía `retry: 3000`.
- **Autenticación por cookie:** `EventSource` no puede enviar cabeceras, así que login y registro también fijan el access token en la cookie `access_token` (`HttpOnly; SameSite=Lax; Path=/boards`). **Solo** el stream la acepta (también acepta Bearer). El resto de la API sigue exigiendo la cabecera `Authorization`, de modo que la cookie no permite CSRF sobre rutas que escriben, y el stream es de solo lectura y está protegido por CORS.
- **Permiso:** cualquier rol que pueda leer el board (viewer o más), con el mismo `requireAccess` y el mismo 403 que el resto. **Cada heartbeat (`: ping`, cada 25 s) vuelve a comprobar el acceso**: si al usuario lo quitan del workspace, su stream se cierra en el siguiente ping.
- **Timeouts:** el servidor tiene `WriteTimeout` de 30 s para las respuestas normales. El stream lo reemplaza en su propia conexión con `http.ResponseController`: antes de cada escritura fija un plazo de 10 s y después hace `Flush`. El stream puede durar indefinidamente, pero una conexión muerta se detecta en la siguiente escritura. Un test lo comprueba con un `WriteTimeout` de 150 ms: sin el `ResponseController`, el stream se corta.
- **Fin limpio:** el stream termina cuando se cancela el contexto (el cliente se desconecta), cuando el hub cierra el canal o al apagar el servidor (`RegisterOnShutdown(hub.Close)`), y siempre cancela su suscripción.
- **Varias instancias:** con `EVENTS_BACKEND=postgres` los eventos y la presencia se comparten por `LISTEN/NOTIFY` (decisión 8).
- **Límites conocidos** (todos, con las cifras de la prueba de carga, en la decisión 9): los ids no son consecutivos por board (la secuencia es de toda la tabla); solo crecen. El access token se comprueba al conectar, así que un stream abierto sobrevive a su caducidad hasta que se reconecta. Cuando exista `/auth/refresh`, el cliente renovará la cookie antes de reconectar.

## 4. Autenticación: JWT de acceso corto y refresh token opaco en cookie

- **Contraseñas** con **argon2id** (parámetros mínimos de OWASP: 19 MiB, 2 iteraciones, 1 hilo) en formato PHC (`$argon2id$v=19$m=…,t=…,p=…$sal$hash`). Los parámetros viajan en el hash, así que se pueden endurecer sin invalidar los existentes. La contraseña debe tener entre 8 y 128 caracteres; el máximo acota el coste de hashear por petición.
- **Access token:** JWT HS256 de 15 minutos con `sub` = id de usuario e `iss` = `tablero`. Al verificar se exige HS256 (rechaza `alg: none` y otros algoritmos), el emisor y la expiración. El cliente lo envía en `Authorization: Bearer`.
- **Refresh token:** valor aleatorio de 32 bytes en la cookie `refresh_token` (`HttpOnly; Secure; SameSite=Lax; Path=/auth`, 30 días). En la base solo se guarda su **SHA-256** (tabla `refresh_tokens`), de modo que una fuga de la base no da sesiones válidas. `Path=/auth` evita que viaje en el resto de peticiones.
- **Login sin pistas:** cualquier fallo (correo inexistente, contraseña incorrecta, campos vacíos) responde el mismo `401` con el mismo cuerpo, "Correo o contraseña incorrectos". Si el correo no existe se verifica igualmente un hash ficticio, para que el tiempo de respuesta no delate qué cuentas existen.
- **Pendiente:** `/auth/refresh` (rotación del refresh token) y `/auth/logout` (revocación); la tabla ya tiene `revoked_at` para ello.

**Por qué:** un access token corto sin estado evita consultar la base en cada petición, y el refresh token largo queda fuera del alcance de JavaScript (`HttpOnly`) y es revocable porque vive en la base. El registro sí revela si un correo ya existe (409); evitarlo exigiría un flujo con confirmación por correo.

## 5. Roles y autorización por workspace

Los permisos se definen por **rol dentro de cada workspace** (`admin`, `member`, `viewer`) en una única matriz del dominio (`internal/domain/role.go`):

| Acción | admin | member | viewer |
|---|---|---|---|
| Leer workspace, miembros, boards, tareas y actividad | ✓ | ✓ | ✓ |
| Crear, editar, mover y borrar tareas | ✓ | ✓ | |
| Crear, editar y borrar etiquetas de un board | ✓ | ✓ | |
| Gestionar boards y columnas (crear, renombrar, borrar, reordenar) | ✓ | | |
| Gestionar miembros y ajustes | ✓ | | |

Lo que no está en la matriz se deniega. Gestionar boards y columnas es solo de admin porque la definición original de `member` se limita a tareas; cambiarlo es una línea de la matriz.

- **Un solo punto de control:** toda ruta bajo un workspace pasa por el middleware `requireAccess`. El middleware resuelve el workspace desde el path o a través del board, la columna, la tarea o la etiqueta indicados, y le pide a `workspace.Authorizer` (capa app) que lea el rol del usuario y aplique la matriz. El resultado es un `domain.Access` que el handler entrega al caso de uso, y cada caso de uso vuelve a exigir con `Access.Require` la acción que necesita (decisión 7).
- **403 sin revelar existencia:** si el usuario no es miembro, si el recurso no existe o si el id no es válido, la respuesta es la misma `403 "No tienes acceso a este workspace"`, idéntica byte a byte. Un miembro sin permiso recibe otro 403, "Tu rol no permite esta acción", porque a él no se le revela nada nuevo. Por eso los recursos inexistentes dan 403 y no 404.
- **Siempre al menos un admin:** cambiar roles y quitar miembros se hace en una transacción que bloquea la fila del workspace (`SELECT … FOR NO KEY UPDATE`), vuelve a comprobar el rol de quien actúa (por si lo degradaron entre la autorización y la transacción) y cuenta admins antes de escribir. Si el cambio dejaría el workspace sin admin, responde `409 last_admin`. Así, dos admins que se degradan mutuamente a la vez no pueden dejarlo sin ninguno.

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

## 7. Arquitectura hexagonal (puertos y adaptadores)

El backend se organiza en tres anillos, y las dependencias solo apuntan hacia dentro:

```mermaid
flowchart LR
    subgraph adapters["internal/adapters"]
        http["http<br/>(router, handlers, middleware)"]
        postgres["postgres<br/>(repositorios, TxManager, sqlc)"]
        token["token (JWT)"]
        hasher["hasher (argon2id)"]
        events["events (Hub, Noop, Memory)"]
    end
    subgraph app["internal/app"]
        auth["auth"]
        workspace["workspace<br/>(+ Authorizer)"]
        board["board"]
        task["task"]
    end
    domain["internal/domain<br/>entidades, reglas, matriz de roles, errores"]
    main["cmd/api/main.go<br/>composition root"]

    http -->|llama casos de uso| app
    postgres -. implementa puertos .-> app
    token -. implementa puertos .-> app
    hasher -. implementa puertos .-> app
    events -. implementa puertos .-> app
    app --> domain
    adapters --> domain
    main --> adapters
    main --> app
```

- **`internal/domain`** tiene las entidades (User, Workspace, Board, Column, Task, Label…), las reglas puras (validaciones, posiciones fraccionarias, diff de una edición, la matriz de roles y `Access.Require`) y los errores de dominio (`ErrConflict` y `ConflictError` con el estado actual, `ErrForbidden`, `ErrNotMember`, `ErrXNotFound`, `ValidationError`). Solo importa la librería estándar. Por eso tiene su propio `ID` (`[16]byte`, la misma representación que `google/uuid`): los adaptadores convierten entre ambos sin coste.
- **`internal/app/{auth,workspace,board,task}`** contiene los casos de uso. **Cada paquete define los puertos que consume**, pequeños y específicos (por ejemplo, `task.BoardRepository` solo tiene las cuatro operaciones de columnas y etiquetas que las tareas necesitan, aunque `postgres.Boards` implemente más). Los reciben por constructor y no conocen HTTP ni SQL.
  - Puertos: `UserRepository`, `RefreshTokenRepository`, `PasswordHasher`, `TokenIssuer`, `Clock`, `WorkspaceRepository`, `BoardRepository`, `LabelRepository`, `TaskRepository`, `ActivityRepository`, `TxManager` y `EventPublisher`.
- **`internal/adapters`** implementa los puertos (`postgres`, `token`, `hasher`, `events`) o traduce al mundo exterior (`http`). `postgres` mapea las filas de sqlc a entidades; el código generado vive en `adapters/postgres/db` y no sale de ahí. `http` traduce errores de dominio a status: `ErrConflict` → 409 con `current`, `ErrForbidden`/`ErrNotMember` → los dos 403, `ErrXNotFound` → 404, `ValidationError` → 422.
- **`cmd/api/main.go`** es la raíz de composición: construye los adaptadores y se los pasa a los casos de uso a mano, sin frameworks de inyección de dependencias.

Detalles que importan:

- **Transacciones:** `TxManager.InTx(ctx, fn)` abre la transacción y la pasa en el `context`. Todos los repositorios de `postgres` la usan si está presente, así que la escritura de la tarea y su entrada de `activity_log` hacen commit o rollback juntas sin que el caso de uso vea `pgx`. `InReadTx` da un snapshot de solo lectura (el GET del board, el 409). Las llamadas anidadas se unen a la transacción exterior.
- **Autorización:** se decide en la capa app. `workspace.Authorizer` aplica la matriz del dominio y devuelve un `domain.Access`, y cada caso de uso exige su acción sobre él. El middleware HTTP pide el `Access` **antes de leer el cuerpo**, igual que antes de esta refactorización, para que un no miembro reciba siempre el mismo 403 y nunca un 400 o un 422 que le dé pistas. Los cambios de membresía vuelven a leer el rol bajo el bloqueo, por si el `Access` quedó obsoleto.
- **Eventos:** los casos de uso publican un `domain.Event` (derivado de la entrada de actividad, con su id) **después** del commit, nunca si hubo rollback. El adaptador es `events.Hub`, que alimenta el stream SSE (decisión 3). Sustituirlo, por ejemplo por un bus compartido entre instancias, no toca los casos de uso.
- **Comprobación en CI:** golangci-lint ejecuta `depguard` (`backend/.golangci.yml`):
  - `domain` solo puede importar la stdlib.
  - `app` solo la stdlib y `domain`.
  - Nadie fuera de `adapters/postgres` puede importar los tipos de sqlc.
  - `adapters/http` no puede importar `adapters/postgres` ni `pgx`.

  Que `domain` importe un adaptador además es un ciclo de imports, que el compilador ya rechaza.
- **Tests:**
  - Los casos de uso se prueban con fakes en memoria de sus puertos (`internal/app/*/…_test.go`): conflicto de versión entre dos clientes, matriz de roles por caso de uso, rollback si falla la actividad y eventos solo tras el commit.
  - Los repositorios se prueban contra Postgres real (`internal/adapters/postgres/*_test.go`, Testcontainers): guarda de versión, bloqueos, vecinos, rollback del `TxManager`.
  - Los tests HTTP de extremo a extremo siguen igual. Solo cambió cómo se cablean.

**Por qué:** la lógica que más importa (concurrencia optimista, reglas de responsable y etiquetas, autorización, último admin) queda en código sin infraestructura, que se prueba en milisegundos con fakes. La base de datos y HTTP se pueden cambiar o ampliar (como el SSE) sin tocarla. Definir los puertos en el paquete que los consume mantiene las interfaces pequeñas, y la regla de dependencias se comprueba en CI en vez de depender de la disciplina.

## 8. Varias instancias: eventos y presencia por `LISTEN/NOTIFY`

Con `EVENTS_BACKEND=postgres` (el valor por defecto es `memory`), el `EventPublisher` de los casos de uso es `postgres.EventBus` en lugar del Hub. Cada instancia sigue teniendo su Hub, que alimenta los streams de sus propios clientes.

- **Publicar:** el bus entrega el evento al Hub local y hace `pg_notify('tablero_events', …)` con un payload mínimo: `{"k":"e","o":<instancia>,"b":<board>,"id":<id>}`. La entidad no viaja, por el límite de 8000 bytes de `NOTIFY`, y además ya está en `activity_log`.
- **Escuchar:** cada instancia hace `LISTEN` con una **conexión dedicada** de pgx, fuera del pool (`application_name = tablero-events-<instancia>`). Las notificaciones de otra instancia, si hay suscriptores locales de ese board, se resuelven leyendo la fila de `activity_log` y entregándola al Hub. Las propias se ignoran, porque ya se entregaron.
- **Reconexión:** si la conexión cae, se reintenta con backoff exponencial (100 ms → 10 s, ±25 %). Tras cada reconexión hay un **resync**: se despiertan todos los streams locales y cada uno lee del log lo posterior a su último id. Así recupera lo que se perdió mientras no escuchaba, sin huecos ni duplicados (decisión 3). Lo mismo ocurre al arrancar.
- **Red de seguridad:** cada heartbeat del stream (25 s) también vuelve a leer el log. Una notificación perdida retrasa la entrega como mucho un heartbeat; nunca la pierde.

**Presencia entre instancias: el mecanismo más simple sin tabla.** Cada instancia es la fuente de verdad de sus propias conexiones. La presencia global es la **unión** de la local y de lo que anuncian las demás, cada anuncio con caducidad.

- Cuando un usuario entra o sale **en esta instancia** (la salida, tras su retraso de 4 s), se envía `{"k":"p",…,"u":{id,nombre},"on":true|false}`.
- Cada 10 s hay un **latido** con el conjunto completo por board, en varias partes si se acerca a los 8000 bytes. El latido **reemplaza** lo que esa instancia había anunciado.
- Lo que una instancia no confirma en 30 s (3 latidos) se olvida, y se emite `presence.left`. Así se cubre una instancia que muere sin despedirse.
- Al arrancar y al reconectar, una instancia envía `{"k":"h"}` (hello) y las demás responden con su latido en el acto. Una instancia nueva conoce la presencia en milisegundos, no en 10 s.
- `presence.joined` y `presence.left` se emiten a los clientes locales solo cuando cambia la unión. Si Ana está conectada en A y en B y cierra A, nadie la ve salir.

**Por qué así:** se descartó una tabla de presencia. Pediría escrituras en cada conexión y desconexión, más un barrido de filas huérfanas cuando una instancia muere, y la presencia ya se había definido como derivada del canal. Con latidos y caducidad, el estado se corrige solo aunque se pierda un mensaje, y el costo es un `NOTIFY` pequeño por board con usuarios cada 10 s.

**Límites:**
- Durante una caída del listener, la presencia remota puede quedar desactualizada hasta el resync, y caduca a los 30 s si la caída es larga.
- `NOTIFY` no es duradero: si una notificación se pierde, lo que la cubre es el log (para los eventos) o el siguiente latido (para la presencia).

El test de integración levanta dos instancias en el mismo proceso, cada una con su Hub, su bus y su servidor, sobre la base de Testcontainers. Comprueba:
- que un evento publicado en una llega al cliente de la otra;
- la presencia entre ellas, incluida una instancia tardía y una que muere;
- que, si se corta la conexión del listener y se pierde una notificación, el cliente recibe el evento tras la reconexión.

## 9. Tiempo real: por qué SSE, cómo se recupera, métricas y límites

La decisión 3 describe cómo está hecho el stream y la 8 cómo se reparte entre instancias. Esta reúne por qué se eligió así, cómo se mide y hasta dónde llega. El contrato de los mensajes está en [`EVENTS.md`](EVENTS.md).

### SSE frente a WebSockets

| | SSE (elegido) | WebSockets |
|---|---|---|
| Dirección | Servidor → cliente. | Bidireccional. |
| Protocolo | HTTP normal (`GET` + `text/event-stream`): mismos middlewares, CORS, logs, proxies y `requireAccess` que el resto de la API. | Upgrade a otro protocolo: autenticación, autorización y límites aparte, y proxies que deben soportarlo. |
| Reconexión | La hace el navegador (`retry`) y envía `Last-Event-ID` solo. | A mano, incluido decidir desde dónde seguir. |
| Escrituras | Siguen por REST, con la guarda de `version` y el 409 con `current` (decisión 1). | Tentación de un segundo camino de escritura con su propio manejo de conflictos. |
| Autenticación | `EventSource` no envía cabeceras: hace falta la cookie `access_token`, aceptada solo en el stream. | El handshake del navegador tampoco permite cabeceras propias; mismo problema. |
| Conexiones por navegador | Con HTTP/1.1, máximo 6 por origen, compartidas con el resto de peticiones. Con HTTP/2 se multiplexan. | Una conexión aparte, sin ese límite. |
| Datos | Solo texto (UTF-8). | Texto o binario. |

**Por qué:** en un kanban, el tiempo real consiste en que los demás vean los cambios. Todo lo que el cliente envía ya tiene su endpoint REST con concurrencia optimista, así que un canal de vuelta no aporta nada y duplicaría la lógica de conflictos. SSE reutiliza la autenticación, la autorización y la observabilidad de la API, y la reconexión con `Last-Event-ID` es exactamente el punto de reanudación que da el log.

**Cuándo replantearlo:** si llegan funciones de alta frecuencia cliente → servidor (cursores en vivo, edición colaborativa de un mismo texto) o si hay que servir sobre HTTP/1.1 a usuarios con muchas pestañas abiertas.

### Reconexión y resync

Un único mecanismo cubre el vivo, la reconexión, el cliente lento y las notificaciones perdidas: **cada stream guarda el id de lo último que envió y siempre lee del log lo posterior**. El hub y `NOTIFY` solo despiertan; nunca son la fuente.

1. **Al conectar:** suscribe y *después* lee el log, para que nada quede entre el reenvío y el vivo. Sin `Last-Event-ID` empieza en vivo.
2. **Al reconectar** con `Last-Event-ID`: reenvía en orden lo posterior. Si son más de 500, o el id no puede ser de este board, envía `resync` (con `id` = el último evento) y el cliente recarga con `GET /boards/{id}`.
3. **Cliente lento:** si su buffer (64) se llena, el hub lo da de baja en vez de bloquear la publicación o crecer sin límite. El stream se cierra, `EventSource` reconecta a los 3 s y vuelve al paso 2.
4. **Notificaciones perdidas** (varias instancias): cada heartbeat (25 s) vuelve a leer el log, y tras reconectar el listener se despiertan todos los streams. El retraso máximo es un heartbeat; no se pierde nada.

**Por qué 500:** más allá, reenviar cuesta más que recargar el board, que son siempre cinco consultas (decisión 6) y deja al cliente en un estado conocido. **Por qué expulsar al lento:** un buffer sin límite convierte un cliente atascado en memoria del servidor, y bloquear haría que un móvil con mala red frenara las escrituras de todos. Como reconectar no pierde nada, expulsar es barato.

### Presencia sin tabla

La presencia se deriva de los streams abiertos. El hub cuenta conexiones por board y usuario, con 4 s de gracia al cerrar la última para que una recarga no se vea como salida y entrada. Entre instancias, cada una anuncia lo suyo por `NOTIFY` (cambios y un latido cada 10 s que caduca a los 30 s), y cada hub muestra la unión (decisión 8).

**Por qué sin tabla:** una tabla pediría una escritura por conexión y desconexión, y un barrido de filas huérfanas cuando una instancia muere sin limpiar. La presencia ya está definida como "quién tiene el stream abierto", y esa información vive en memoria en la instancia que sirve el stream. Con latidos y caducidad, el estado se corrige solo aunque se pierda un mensaje. El precio es que la presencia no sobrevive a un reinicio, lo cual es correcto: al reiniciar, esas conexiones también se cortan.

### Métricas (`GET /metrics`)

Formato de texto de Prometheus con un registro propio de menos de 300 líneas (`adapters/metrics`) en lugar de `client_golang`. Son cuatro métricas, una de ellas con una sola etiqueta, y así no entra una dependencia con su árbol de transitivas.

| Métrica | Dónde se mide |
|---|---|
| `tablero_sse_connections` (gauge) | Suscriptores del hub en el momento del scrape. |
| `tablero_events_published_total{type}` (counter) | `events.Counting`, que envuelve al publicador de los casos de uso: solo los cambios confirmados en esta instancia, no los que llegan de otras. |
| `tablero_sse_slow_clients_dropped_total` (counter) | El hub, cuando el buffer de un suscriptor está lleno. |
| `tablero_sse_delivery_seconds` (histogram) | El hub pone `PublishedAt` al publicar (tras el commit) y el handler observa `now − PublishedAt` tras el `Flush` de cada mensaje. Solo eventos en vivo: el reenvío de una reconexión no se mide, porque mediría cuánto estuvo desconectado el cliente. |
| `go_goroutines`, `go_memstats_*` | `runtime.ReadMemStats`, como mucho una vez cada 100 ms. |

Aproximaciones:
- Con varias instancias, un evento remoto se sella al llegar a esta, así que la medida no incluye el `NOTIFY` ni la lectura de la fila.
- Un evento que el heartbeat lee del log antes de que llegue su aviso no se mide (6 de 150 000 en la prueba).

### Prueba de carga

`backend/cmd/loadtest` registra un usuario desechable, crea un board con 20 tareas, abre N streams SSE (con `Authorization: Bearer`) y mueve tareas por turnos a un ritmo fijo, de una en una.

- **Latencia:** se mide en el cliente, desde justo antes de enviar el `PATCH /tasks/{id}/move` hasta que cada stream lee su `task.moved`. Incluye la transacción, así que es una cota superior de lo que mide el servidor.
- **Memoria:** se lee de `/metrics` antes de conectar, con los clientes conectados en reposo y en el pico durante la carga.

```sh
cd backend && go run ./cmd/loadtest -url http://localhost:8080 -clients 500 -duration 30s -rate 10
```

Resultados del 2026-10-25, con `EVENTS_BACKEND=memory`. API, Postgres 16 (Docker) y generador corrían en la misma máquina: Windows 11 con 16 CPU.

| | 500 clientes, 10 mov/s, 30 s | 500 clientes, 40 mov/s pedidos, 20 s |
|---|---|---|
| Movimientos | 300 (10/s) | 459 (**22,9/s**: el techo, ver límites) |
| Eventos recibidos | 150 000 / 150 000 | 229 500 / 229 500 |
| Latencia cliente p50 / p90 / p99 / max | 30 / 44 / 54 / 66 ms | 60 / 75 / 95 / 117 ms |
| `PATCH /move` p50 / p99 | 12 / 26 ms | 42 / 65 ms |
| Servidor, publicar → flush (media; p99) | 18,7 ms; ≤ 50 ms | 18,2 ms; ≤ 50 ms |
| Expulsados por lentos / reconexiones | 0 / 0 | 0 / 0 |

Memoria del servidor en la primera ejecución (proceso recién arrancado):

| | Antes | 500 conectados | Pico con carga |
|---|---|---|---|
| Heap en uso | 20,7 MiB | 22,8 MiB | 38,6 MiB |
| Stacks | 0,3 MiB | 12,1 MiB | 12,9 MiB |
| Memoria pedida al SO (`sys`) | 31,1 MiB | 62,8 MiB | 71,0 MiB |
| Goroutines | 7 | 1007 | 1008 |

Cada conexión cuesta unos **28 KiB** (heap y stack) y dos goroutines: la del handler y la de lectura de `net/http`. El heap de partida ya incluye unos 19 MiB del hash ficticio de argon2id que se calcula al arrancar (decisión 4).

### Límites conocidos

- **Una lectura del log por stream y por evento.** Cada aviso hace que *cada* stream del board lea `activity_log`: con 500 clientes, un movimiento son 500 consultas, y compiten por el pool de pgx con las escrituras. Eso fija el techo medido, unos 23 movimientos/s secuenciales con 500 espectadores, y la mediana del `PATCH` pasa de 12 a 42 ms. No se pierde nada, pero las escrituras se frenan.
  - La mejora natural es que el hub lea el log **una vez por board** y reparta las filas a sus streams, aplicando a cada uno su cursor y `exclude_self`.
  - No se ha hecho aún porque complica el reenvío, que sí es por stream.
- **Memoria lineal:** unos 28 KiB por conexión, es decir, unos 280 MiB para 10 000 streams en una instancia, más sus descriptores de archivo.
- **Ráfagas mayores que el buffer:** si un cliente acumula 64 avisos sin vaciarse, se le expulsa y reconecta. Con un evento por cambio no pasó en la prueba, pero una importación masiva al ritmo de la base podría provocar reconexiones en cadena. Se recuperan solas, con `resync` si pasan de 500.
- **HTTP/1.1 en el navegador:** 6 conexiones por origen, y cada pestaña con un board abierto ocupa una de forma permanente. En producción conviene servir la API con HTTP/2.
- **Token y acceso:** el access token se valida al conectar, y un stream abierto sobrevive a su caducidad hasta que reconecta. La pérdida de acceso se detecta en el siguiente heartbeat, hasta 25 s después.
- **Ids no consecutivos:** la secuencia es de toda la tabla, así que un salto de id no indica un hueco. El cliente no debe intentar detectar pérdidas por su cuenta.
- **`/metrics` sin autenticación** y con valores por instancia. Debe quedar solo en la red interna (por el proxy o un puerto aparte); con varias instancias, Prometheus agrega los valores.
- **Lo que la prueba no cubre:**
  - Todos los clientes son el mismo usuario, porque aún no hay invitaciones a un workspace, así que no se prueban ráfagas de `presence.joined` y `presence.left`.
  - Tampoco hay red real ni varias instancias: generador, API y base comparten máquina, así que las latencias son optimistas frente a un despliegue.
