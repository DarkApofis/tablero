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
