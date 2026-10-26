# Catálogo de eventos en tiempo real

`GET /boards/{id}/events` es un stream `text/event-stream` (Server-Sent Events) con los cambios de un board. Este documento describe cada mensaje que puede llegar y qué debe hacer el cliente con él. El contrato formal está en [`openapi.yaml`](openapi.yaml) (`x-sse-events` del stream y los esquemas `StreamEvent`, `*Payload`, `StreamResync`, `PresenceSnapshot`, `PresenceChange`). El porqué del diseño está en [`DECISIONS.md`](DECISIONS.md) (decisiones 3, 8 y 9).

## Conexión

```js
const es = new EventSource(`${API}/boards/${boardId}/events`, { withCredentials: true });
es.addEventListener("task.moved", (e) => apply(JSON.parse(e.data)));
```

- **Autenticación:** la cookie `access_token` que fijan `/auth/login` y `/auth/register` (`EventSource` no puede enviar cabeceras), o `Authorization: Bearer` desde clientes que sí pueden.
- **Permiso:** leer el board (viewer o más). Sin acceso: el mismo `403` que el resto de la API.
- **Parámetros:**
  - `Last-Event-ID` (cabecera; `EventSource` la envía sola al reconectar) o `?last_event_id=` (para un `EventSource` nuevo que continúa donde se quedó otro).
  - `?exclude_self=true` o `X-Exclude-Self: true`: no recibir los eventos de los propios cambios. Útil si el cliente ya aplicó la respuesta del `PATCH`.

## Orden de los mensajes

```
retry: 3000                       ← cuánto esperar antes de reconectar

event: presence.snapshot          ← quién está conectado (sin id)
data: {...}

id: 41                            ← reenvío de lo perdido, solo si se envió Last-Event-ID
event: task.updated
data: {...}

id: 42                            ← en vivo
event: task.moved
data: {...}

: ping                            ← cada 25 s (comentario; EventSource lo ignora)

event: presence.joined            ← cuando alguien entra o sale (sin id)
data: {...}
```

## Eventos de dominio

Un mensaje por cada cambio confirmado del board, en el orden en que se confirmaron. Llevan `id` SSE: el id de su entrada en `activity_log` (`GET /boards/{id}/activity` devuelve las mismas entradas). Los ids crecen dentro del board, pero **no son consecutivos** (la secuencia es de toda la tabla): no se puede deducir un hueco de un salto de id.

El `data` de todos tiene la misma envoltura (`StreamEvent`):

```json
{
  "id": 42,
  "type": "task.moved",
  "board_id": "6f1c…",
  "actor_id": "a3b9…",
  "version": 4,
  "payload": { "…": "depende de type" },
  "created_at": "2026-10-24T18:03:11.402Z"
}
```

| Campo | |
|---|---|
| `id` | Igual que el `id` SSE. |
| `actor_id` | Quién hizo el cambio; `null` si ese usuario ya no existe. |
| `version` | Versión de la tarea **tras** el cambio en los eventos `task.*`; `null` en columnas y etiquetas. Sirve para descartar lo que ya se tiene: si la tarea local tiene esa versión o una mayor, el evento no aporta nada. |
| `payload` | Lo mismo que el `payload` de la actividad, según `type` (tabla siguiente). |

| `event` | Cuándo | `payload` | Qué hacer en el cliente |
|---|---|---|---|
| `task.created` | Se crea una tarea. | `TaskCreatedPayload`: la tarea completa (`task_id`, `title`, `description`, `column_id`, `position`, `version` = 1, `priority`, `due_date`, `assignee_id`, `label_ids`). | Insertarla en su columna por `position`. |
| `task.updated` | Se edita una tarea (título, descripción, prioridad, fecha, responsable, etiquetas) o se desarchiva. También cuando quitar a un miembro la deja sin responsable. | `TaskUpdatedPayload`: `task_id`, `title` (tras el cambio), `version` y `changes` con solo los campos que cambiaron, cada uno `{from, to}`. | Aplicar `changes[*].to`. Si `archived_at.to` es `null`, la tarea vuelve a mostrarse. |
| `task.archived` | Una edición que archiva la tarea. | `TaskUpdatedPayload` (con `changes.archived_at`). | Ocultarla, salvo que se muestren archivadas. |
| `task.moved` | Se mueve una tarea, dentro de su columna o a otra. | `TaskMovedPayload`: `task_id`, `title`, `version`, `from` y `to` (`{column_id, position}`) y, si hubo que reequilibrar la columna destino, `rebalanced` con la nueva `position` de cada tarea de esa columna. | Moverla a `to`. Si viene `rebalanced`, aplicar todas esas posiciones (esas tareas **no** cambian de versión). |
| `task.deleted` | Se borra una tarea. | `TaskDeletedPayload`: `task_id`, `title`, `column_id`, `version` (la última). | Quitarla. |
| `column.created` | Se crea una columna. | `ColumnCreatedPayload`: `column_id`, `name`, `position`. | Insertarla por `position`, vacía. |
| `column.updated` | Se renombra una columna. | `ColumnUpdatedPayload`: `column_id`, `name` (tras el cambio), `changes.name`. | Renombrarla. |
| `column.reordered` | Se mueve una columna. | `ColumnReorderedPayload`: `column_id`, `name`, `from`, `to` (posiciones) y, si hubo que reequilibrar, `rebalanced` con la nueva `position` de cada columna. | Reordenar. |
| `column.deleted` | Se borra una columna, con sus tareas. | `ColumnDeletedPayload`: `column_id`, `name`, `deleted_task_ids`. | Quitar la columna y esas tareas. Las tareas **no** reciben su propio `task.deleted`. |
| `label.changed` | Se crea, edita o borra una etiqueta del board. | `LabelChangedPayload`: `action` (`created`, `updated`, `deleted`), `label` (`{id, name, color}`, tras el cambio o antes de borrarla) y, en `updated`, `changes`. | Actualizar el catálogo de etiquetas. En `deleted`, quitarla de todas las tareas: sus versiones **no** cambian y no llega un `task.updated` por cada una. |

Ejemplos de `payload`:

```json
// task.moved con reequilibrio
{ "task_id": "t1", "title": "Diseñar login", "version": 4,
  "from": { "column_id": "c1", "position": 3072 },
  "to":   { "column_id": "c2", "position": 2048 },
  "rebalanced": [ { "task_id": "t7", "position": 1024 }, { "task_id": "t1", "position": 2048 },
                  { "task_id": "t9", "position": 3072 } ] }

// task.updated
{ "task_id": "t1", "title": "Diseñar login", "version": 5,
  "changes": { "priority": { "from": "medium", "to": "high" },
               "label_ids": { "from": [], "to": ["l2"] } } }

// label.changed
{ "action": "updated", "label": { "id": "l2", "name": "Bug", "color": "#e5484d" },
  "changes": { "color": { "from": "#ff0000", "to": "#e5484d" } } }
```

Un `type` que el cliente no conozca (de una versión más nueva de la API) se debe ignorar, o tratar como un `resync` si el cliente prefiere no arriesgarse.

## `resync`

```
id: 57
event: resync
data: {"last_event_id":57}
```

Llega en lugar del reenvío cuando el cliente se perdió **más de 500** eventos, o cuando el `Last-Event-ID` que envió no puede ser de este board (es mayor que el último, o ya no está en su registro). También llega en vivo cuando el contenido del board se restaura entero (el reset del modo demo), porque su registro anterior se borra. El cliente debe descartar su estado, recargar con `GET /boards/{id}` y seguir aplicando lo que llegue: el stream continúa desde `last_event_id`, y como el mensaje lleva `id`, una reconexión posterior parte de ahí.

Lo que llegue entre el `resync` y la respuesta del `GET` se puede aplicar o descartar por `version` (tareas). Como el `GET` es posterior, para columnas y etiquetas basta con aplicarlo encima.

## Presencia

Los mensajes de presencia **no llevan `id`**. Por eso no mueven el `Last-Event-ID` del navegador ni se reenvían al reconectar: cada conexión recibe el estado completo en su `presence.snapshot`.

| `event` | Cuándo | `data` |
|---|---|---|
| `presence.snapshot` | Lo primero tras `retry`, en cada conexión. | `{"type", "board_id", "users": [{"id", "name"}]}`, ordenados por nombre. Incluye al propio usuario. |
| `presence.joined` | Alguien abre su primera conexión al board. | `{"type", "board_id", "user": {"id", "name"}}` |
| `presence.left` | Se cierra la última conexión de alguien y pasan 4 s sin que vuelva. | Igual que `joined`. |

- Varias pestañas (o instancias de la API) de un mismo usuario son **una** presencia.
- Una recarga de página no produce `left` + `joined`: el usuario vuelve antes de los 4 s.
- La conexión propia no recibe su `joined`.
- `GET /boards/{id}/presence` devuelve lo mismo que el snapshot sin abrir un stream.

## Fin del stream y reconexión

El servidor cierra el stream cuando:

- el usuario pierde el acceso al board (se comprueba en cada `: ping`, cada 25 s);
- el cliente se queda tan atrás que llena su buffer de 64 mensajes (**cliente lento**): se le expulsa para no frenar a los demás;
- el servidor se apaga.

En los dos últimos casos el cliente debe reconectar. `EventSource` lo hace solo a los 3 s (`retry`) enviando `Last-Event-ID`, y recibe en orden lo que se perdió, sin huecos ni duplicados, o un `resync`. Si el cierre fue por falta de acceso, al reconectar recibe un `403` y `EventSource` deja de intentarlo (`readyState` = `CLOSED`).

Un cliente propio (no `EventSource`) debe imitar ese comportamiento: guardar el último `id` recibido, esperar `retry` ms y reconectar con `Last-Event-ID`.

El access token se valida al conectar. Un stream abierto sobrevive a su caducidad, pero la reconexión necesita un token vigente en la cookie.
