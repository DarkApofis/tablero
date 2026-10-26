package domain

import "encoding/json"

// Presence is a user connected to a board's event stream. It is derived
// from the open streams and lives only in memory: nothing is stored.
type Presence struct {
	UserID ID
	Name   string
}

// Presence events. They are ephemeral: not in the activity log, not
// replayed on reconnect, and sent without an SSE id.
const (
	EventPresenceJoined   EventType = "presence.joined"
	EventPresenceLeft     EventType = "presence.left"
	EventPresenceSnapshot EventType = "presence.snapshot"
)

// EventWake is internal and never sent to clients: it tells a board's
// streams to read the log again (after a listener reconnects, say).
const EventWake EventType = "wake"

// EventReset is internal too: the board's content was replaced wholesale and
// its old log entries deleted (the demo's periodic restore). Streams answer it
// with a resync, since the log no longer explains how the client's copy got
// to the new state.
const EventReset EventType = "reset"

// IsPresence reports whether t is a presence event rather than a change of
// the board.
func (t EventType) IsPresence() bool {
	return t == EventPresenceJoined || t == EventPresenceLeft || t == EventPresenceSnapshot
}

// PresenceUser is how presence events show a user.
type PresenceUser struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// PresencePayload is the payload of presence.joined and presence.left.
type PresencePayload struct {
	User PresenceUser `json:"user"`
}

// PresenceEvent builds a presence.joined or presence.left event.
func PresenceEvent(typ EventType, boardID ID, p Presence) Event {
	data, _ := json.Marshal(PresencePayload{User: PresenceUser{ID: p.UserID, Name: p.Name}})
	return Event{Type: typ, BoardID: boardID, ActorID: &p.UserID, Payload: data}
}
