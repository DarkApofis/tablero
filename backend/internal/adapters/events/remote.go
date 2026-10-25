package events

import (
	"time"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// What a Hub offers to share events and presence with other API instances
// (postgres.EventBus uses it). In memory mode none of it is used.

// PresenceObserver hears about changes of this instance's own presence: a
// user's first connection to a board (present) and, after the leave delay,
// their last one going away. It is called with the hub's lock held, so it
// must not block or call back into the hub.
type PresenceObserver func(boardID domain.ID, who domain.Presence, present bool)

// ObservePresence sets the observer; call it before the hub is used.
func (h *Hub) ObservePresence(fn PresenceObserver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.observer = fn
}

// remoteSet is who another instance says is connected to a board through it.
type remoteSet struct {
	users map[domain.ID]domain.Presence
	seen  time.Time // last time the instance mentioned this board
}

// SetRemotePresence records that instance has users connected to boardID.
// With replace the set becomes exactly users; otherwise they are added.
// Local subscribers get joined/left for users who come or go overall.
func (h *Hub) SetRemotePresence(instance string, boardID domain.ID, users []domain.Presence, replace bool, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	set := h.remoteSetLocked(instance, boardID, now)
	old := set.users
	if replace {
		set.users = map[domain.ID]domain.Presence{}
	}
	for _, u := range users {
		if _, had := old[u.UserID]; !had && !h.isPresentLocked(boardID, u.UserID) {
			h.publishLocked(domain.PresenceEvent(domain.EventPresenceJoined, boardID, u))
		}
		set.users[u.UserID] = u
	}
	if replace {
		for id, u := range old {
			if _, still := set.users[id]; !still && !h.isPresentLocked(boardID, id) {
				h.publishLocked(domain.PresenceEvent(domain.EventPresenceLeft, boardID, u))
			}
		}
	}
	h.dropEmptyLocked(instance, boardID)
}

// RemoveRemotePresence records that who is no longer connected to boardID
// through instance.
func (h *Hub) RemoveRemotePresence(instance string, boardID domain.ID, who domain.Presence, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	set := h.remoteSetLocked(instance, boardID, now)
	if _, had := set.users[who.UserID]; had {
		delete(set.users, who.UserID)
		if !h.isPresentLocked(boardID, who.UserID) {
			h.publishLocked(domain.PresenceEvent(domain.EventPresenceLeft, boardID, who))
		}
	}
	h.dropEmptyLocked(instance, boardID)
}

// ExpireRemotePresence forgets what instances have not confirmed since
// before: an instance that died cannot say its users left.
func (h *Hub) ExpireRemotePresence(before time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for boardID, sets := range h.remote {
		for instance, set := range sets {
			if !set.seen.Before(before) {
				continue
			}
			delete(sets, instance)
			for id, u := range set.users {
				if !h.isPresentLocked(boardID, id) {
					h.publishLocked(domain.PresenceEvent(domain.EventPresenceLeft, boardID, u))
				}
			}
		}
		if len(sets) == 0 {
			delete(h.remote, boardID)
		}
	}
}

// LocalPresence returns this instance's own presence per board (users in
// their leave delay included), for announcing it to the other instances.
func (h *Hub) LocalPresence() map[domain.ID][]domain.Presence {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[domain.ID][]domain.Presence, len(h.presence))
	for boardID, users := range h.presence {
		for _, p := range users {
			out[boardID] = append(out[boardID], p.who)
		}
	}
	return out
}

// WakeAll tells every local stream to read the log again: after missing
// notifications (a listener reconnecting) that recovers what they missed.
func (h *Hub) WakeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for boardID := range h.boards {
		h.publishLocked(domain.Event{Type: domain.EventWake, BoardID: boardID})
	}
}

// --- with h.mu held ---

func (h *Hub) observe(boardID domain.ID, who domain.Presence, present bool) {
	if h.observer != nil {
		h.observer(boardID, who, present)
	}
}

// isPresentLocked reports whether userID is present on boardID overall:
// connected here or through another instance.
func (h *Hub) isPresentLocked(boardID, userID domain.ID) bool {
	if _, ok := h.presence[boardID][userID]; ok {
		return true
	}
	for _, set := range h.remote[boardID] {
		if _, ok := set.users[userID]; ok {
			return true
		}
	}
	return false
}

func (h *Hub) remoteSetLocked(instance string, boardID domain.ID, now time.Time) *remoteSet {
	sets := h.remote[boardID]
	if sets == nil {
		sets = map[string]*remoteSet{}
		h.remote[boardID] = sets
	}
	set := sets[instance]
	if set == nil {
		set = &remoteSet{users: map[domain.ID]domain.Presence{}}
		sets[instance] = set
	}
	set.seen = now
	return set
}

func (h *Hub) dropEmptyLocked(instance string, boardID domain.ID) {
	if len(h.remote[boardID][instance].users) == 0 {
		delete(h.remote[boardID], instance)
	}
	if len(h.remote[boardID]) == 0 {
		delete(h.remote, boardID)
	}
}
