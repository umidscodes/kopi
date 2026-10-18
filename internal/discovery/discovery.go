// Package discovery finds other Kopi devices on the local network.
package discovery

import (
	"context"
	gosync "sync"

	"github.com/umidscodes/kopi/internal/transport"
)

// Event reports a peer appearing (Up) or leaving.
type Event struct {
	Up   bool
	Peer transport.Peer
}

// Discovery announces this device and reports peers until ctx is done.
type Discovery interface {
	Run(ctx context.Context, emit func(Event)) error
}

// FakeLAN is an in-memory network for tests: every device that joins sees every
// other one, and leaving (ctx done) is seen by everyone still present.
type FakeLAN struct {
	mu      gosync.Mutex
	members map[string]*fakeMember
}

type fakeMember struct {
	self transport.Peer
	emit func(Event)
}

func NewFakeLAN() *FakeLAN { return &FakeLAN{members: map[string]*fakeMember{}} }

// Join returns a Discovery for one device on this LAN.
func (l *FakeLAN) Join(self transport.Peer) Discovery { return fakeDiscovery{lan: l, self: self} }

type fakeDiscovery struct {
	lan  *FakeLAN
	self transport.Peer
}

func (d fakeDiscovery) Run(ctx context.Context, emit func(Event)) error {
	l := d.lan
	me := &fakeMember{self: d.self, emit: emit}
	l.mu.Lock()
	others := make([]*fakeMember, 0, len(l.members))
	for _, m := range l.members {
		others = append(others, m)
	}
	l.members[d.self.ID] = me
	l.mu.Unlock()
	for _, m := range others {
		m.emit(Event{Up: true, Peer: d.self})
		emit(Event{Up: true, Peer: m.self})
	}

	<-ctx.Done()

	l.mu.Lock()
	if l.members[d.self.ID] == me {
		delete(l.members, d.self.ID)
	}
	rest := make([]*fakeMember, 0, len(l.members))
	for _, m := range l.members {
		rest = append(rest, m)
	}
	l.mu.Unlock()
	for _, m := range rest {
		m.emit(Event{Up: false, Peer: transport.Peer{ID: d.self.ID}})
	}
	return nil
}
