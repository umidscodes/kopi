package proto

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrStale  = errors.New("kopi: message timestamp outside the accepted window")
	ErrReplay = errors.New("kopi: message already seen")
)

// ReplayGuard rejects messages that are too old (or too far in the future) and
// messages whose ID was already accepted. Seen IDs only need to be remembered for
// the length of the window: anything older is rejected as stale anyway.
type ReplayGuard struct {
	Window time.Duration
	Now    func() time.Time

	mu   sync.Mutex
	seen map[[16]byte]time.Time
}

// NewReplayGuard returns a guard with the given window (e.g. 2 minutes).
func NewReplayGuard(window time.Duration) *ReplayGuard {
	return &ReplayGuard{Window: window, Now: time.Now, seen: map[[16]byte]time.Time{}}
}

// Check returns nil and records m if it is fresh and unseen.
func (g *ReplayGuard) Check(m Message) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.Now()
	if d := now.Sub(m.SentAt); d > g.Window || d < -g.Window {
		return ErrStale
	}
	if _, ok := g.seen[m.ID]; ok {
		return ErrReplay
	}
	// A message can arrive up to Window early (sender clock ahead) and stays fresh until
	// Window after its timestamp, so its ID must be kept for 2*Window after arrival.
	// 3*Window leaves margin.
	for id, t := range g.seen {
		if now.Sub(t) > 3*g.Window {
			delete(g.seen, id)
		}
	}
	g.seen[m.ID] = now
	return nil
}
