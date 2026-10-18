package transport

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strconv"
	gosync "sync"
	"time"
)

// Peer is another Kopi device found on the network.
type Peer struct {
	ID    string
	Name  string
	Addrs []netip.AddrPort
}

// State is what we last learned about a peer.
type State int

const (
	StateUnknown     State = iota // discovered, nothing sent yet
	StateOK                       // last exchange worked
	StateMismatch                 // peer uses a different passphrase (SEC-1)
	StateUnreachable              // last send failed at the network level
)

func (s State) String() string {
	switch s {
	case StateOK:
		return "ok"
	case StateMismatch:
		return "passphrase mismatch"
	case StateUnreachable:
		return "unreachable"
	default:
		return "unknown"
	}
}

// PeerStatus is a peer plus its current state, for the tray.
type PeerStatus struct {
	Peer
	State State
}

// SendFunc delivers one sealed message to one address. Send is the real one.
type SendFunc func(ctx context.Context, addr string, sealed []byte) error

// Hub tracks peers and broadcasts to them. It implements sync.Broadcaster.
type Hub struct {
	// Send is replaceable for tests.
	Send SendFunc
	// SendTimeout bounds one delivery attempt to one peer.
	SendTimeout time.Duration
	// OnChange, if set, is called after the peer list or a peer's state changes.
	OnChange func()

	mu    gosync.Mutex
	peers map[string]*PeerStatus
	wg    gosync.WaitGroup
}

func NewHub() *Hub {
	return &Hub{Send: Send, SendTimeout: IOTimeout, peers: map[string]*PeerStatus{}}
}

// Upsert adds a peer or merges new addresses into a known one.
func (h *Hub) Upsert(p Peer) {
	h.mu.Lock()
	cur, ok := h.peers[p.ID]
	if !ok {
		cur = &PeerStatus{Peer: Peer{ID: p.ID}}
		h.peers[p.ID] = cur
	}
	if p.Name != "" {
		cur.Name = p.Name
	}
	for _, a := range p.Addrs {
		if !slices.Contains(cur.Addrs, a) {
			cur.Addrs = append(cur.Addrs, a)
		}
	}
	// Try IPv4 first: link-local IPv6 often needs zones that don't survive the trip.
	slices.SortStableFunc(cur.Addrs, func(a, b netip.AddrPort) int {
		return boolInt(b.Addr().Unmap().Is4()) - boolInt(a.Addr().Unmap().Is4())
	})
	h.mu.Unlock()
	h.changed()
}

// Remove forgets a peer (it left the network).
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	_, ok := h.peers[id]
	delete(h.peers, id)
	h.mu.Unlock()
	if ok {
		h.changed()
	}
}

// Peers returns a snapshot of all peers, sorted by name.
func (h *Hub) Peers() []PeerStatus {
	h.mu.Lock()
	out := make([]PeerStatus, 0, len(h.peers))
	for _, p := range h.peers {
		cp := *p
		cp.Addrs = slices.Clone(p.Addrs)
		out = append(out, cp)
	}
	h.mu.Unlock()
	slices.SortFunc(out, func(a, b PeerStatus) int {
		if a.Name != b.Name {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return out
}

// MarkMismatchFrom marks every peer with this IP as using a different passphrase.
// Called when an undecryptable message arrives, so the receiver shows it too (SEC-1).
func (h *Hub) MarkMismatchFrom(ip netip.Addr) {
	ip = ip.Unmap()
	h.mu.Lock()
	hit := false
	for _, p := range h.peers {
		for _, a := range p.Addrs {
			if a.Addr().Unmap() == ip {
				p.State = StateMismatch
				hit = true
				break
			}
		}
	}
	h.mu.Unlock()
	if hit {
		h.changed()
	}
}

// ResetStates forgets what we learned about every peer (e.g. after the passphrase
// changed, old "mismatch" verdicts no longer hold).
func (h *Hub) ResetStates() {
	h.mu.Lock()
	for _, p := range h.peers {
		p.State = StateUnknown
	}
	h.mu.Unlock()
	h.changed()
}

// Broadcast sends sealed to every peer in the background. Slow or dead peers never
// block the caller.
func (h *Hub) Broadcast(sealed []byte) {
	for _, p := range h.Peers() {
		h.wg.Add(1)
		go func(p PeerStatus) {
			defer h.wg.Done()
			h.setState(p.ID, h.deliver(p, sealed))
		}(p)
	}
}

// Wait blocks until all in-flight broadcasts finish. Useful in tests and at shutdown.
func (h *Hub) Wait() { h.wg.Wait() }

func (h *Hub) deliver(p PeerStatus, sealed []byte) State {
	if len(p.Addrs) == 0 {
		return StateUnreachable
	}
	for _, a := range p.Addrs {
		ctx, cancel := context.WithTimeout(context.Background(), h.SendTimeout)
		err := h.Send(ctx, dialAddr(a), sealed)
		cancel()
		switch {
		case err == nil:
			return StateOK
		case errors.Is(err, ErrPeerMismatch):
			return StateMismatch
		case errors.Is(err, ErrRejected):
			// Reachable and same passphrase; it just refused this one (e.g. paused race).
			return StateOK
		}
		// Network error: try the next address.
	}
	return StateUnreachable
}

func (h *Hub) setState(id string, s State) {
	h.mu.Lock()
	p, ok := h.peers[id]
	changed := ok && p.State != s
	if ok {
		p.State = s
	}
	h.mu.Unlock()
	if changed {
		h.changed()
	}
}

func (h *Hub) changed() {
	if h.OnChange != nil {
		h.OnChange()
	}
}

func dialAddr(a netip.AddrPort) string {
	ip := a.Addr().Unmap()
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(a.Port())))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
