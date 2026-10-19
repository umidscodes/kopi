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

	"github.com/umidscodes/kopi/internal/proto"
)

// Peer is another Kopi device found on the network.
type Peer struct {
	ID    string
	Name  string
	Addrs []netip.AddrPort
	// Version is the protocol version the peer publishes; 0 = not reported or
	// unreadable, which behaves like our own version (normal states).
	Version int
}

// State is what we last learned about a peer.
type State int

const (
	StateUnknown     State = iota // discovered, nothing sent yet
	StateOK                       // last exchange worked
	StateMismatch                 // peer uses a different passphrase (SEC-1)
	StateUnreachable              // last send failed at the network level
	StateNeedsUpdate              // peer speaks another protocol version (RECENT-21)
)

func (s State) String() string {
	switch s {
	case StateOK:
		return "ok"
	case StateMismatch:
		return "passphrase mismatch"
	case StateUnreachable:
		return "unreachable"
	case StateNeedsUpdate:
		return "needs update"
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
	// RetryDelays are the pauses between attempts when a peer is unreachable, e.g. a
	// laptop whose Wi-Fi is still reconnecting after waking from sleep. A newer clip
	// cancels the retries of an older one; it will be sent anyway.
	RetryDelays []time.Duration

	mu    gosync.Mutex
	peers map[string]*PeerStatus
	// oldFrom holds peers that sent us a frame of another protocol version before
	// discovery reported their version (RECENT-20).
	oldFrom map[string]bool
	gen     uint64 // bumped by every Broadcast
	wg      gosync.WaitGroup
	stop    chan struct{}
	once    gosync.Once
}

// DefaultRetryDelays add up to about 30 s of retrying.
var DefaultRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}

func NewHub() *Hub {
	return &Hub{Send: Send, SendTimeout: IOTimeout, RetryDelays: DefaultRetryDelays,
		peers: map[string]*PeerStatus{}, oldFrom: map[string]bool{}, stop: make(chan struct{})}
}

// Stop cancels pending retries (at shutdown).
func (h *Hub) Stop() { h.once.Do(func() { close(h.stop) }) }

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
	if p.Version != 0 { // a sighting without a version keeps the one we know
		cur.Version = p.Version
		if p.Version == proto.Version {
			delete(h.oldFrom, p.ID)
		}
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
	delete(h.oldFrom, id)
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
		if h.needsUpdate(p) {
			cp.State = StateNeedsUpdate
		}
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
		if h.needsUpdate(p) {
			continue // RECENT-21: never shown as mismatch
		}
		if hasIP(p, ip) {
			p.State = StateMismatch
			hit = true
		}
	}
	h.mu.Unlock()
	if hit {
		h.changed()
	}
}

// MarkNeedsUpdateFrom marks every peer with this IP as speaking another protocol
// version. Called when a frame of another version arrives, which covers an old peer
// whose version discovery hasn't reported yet (RECENT-20, RECENT-21). A peer that
// reports our own version is left alone.
func (h *Hub) MarkNeedsUpdateFrom(ip netip.Addr) {
	ip = ip.Unmap()
	h.mu.Lock()
	hit := false
	for id, p := range h.peers {
		if p.Version == proto.Version || h.needsUpdate(p) {
			continue
		}
		if hasIP(p, ip) {
			h.oldFrom[id] = true
			hit = true
		}
	}
	h.mu.Unlock()
	if hit {
		h.changed()
	}
}

// needsUpdate reports whether p speaks another protocol version. h.mu must be held.
func (h *Hub) needsUpdate(p *PeerStatus) bool {
	return (p.Version != 0 && p.Version != proto.Version) || h.oldFrom[p.ID]
}

func hasIP(p *PeerStatus, ip netip.Addr) bool {
	for _, a := range p.Addrs {
		if a.Addr().Unmap() == ip {
			return true
		}
	}
	return false
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
	h.mu.Lock()
	h.gen++
	gen := h.gen
	h.mu.Unlock()
	for _, p := range h.Peers() {
		if p.State == StateNeedsUpdate {
			continue // it can't read our clips: no send, no retries (RECENT-20)
		}
		h.wg.Add(1)
		go func(p PeerStatus) {
			defer h.wg.Done()
			h.deliverWithRetry(p, sealed, gen)
		}(p)
	}
}

// deliverWithRetry sends to one peer, retrying network failures (T17: copy right
// after the other device wakes up) until it works, a newer clip supersedes this one,
// the peer is removed, or the retries run out.
func (h *Hub) deliverWithRetry(p PeerStatus, sealed []byte, gen uint64) {
	s := h.deliver(p, sealed)
	h.setState(p.ID, s)
	for _, d := range h.RetryDelays {
		if s != StateUnreachable {
			return
		}
		select {
		case <-h.stop:
			return
		case <-time.After(d):
		}
		h.mu.Lock()
		cur, ok := h.peers[p.ID]
		superseded := h.gen != gen
		if ok && h.needsUpdate(cur) {
			ok = false // learned meanwhile that it speaks another version
		}
		if ok {
			p = *cur // pick up addresses learned meanwhile (e.g. Tailscale came back)
			p.Addrs = slices.Clone(cur.Addrs)
		}
		h.mu.Unlock()
		if !ok || superseded {
			return
		}
		s = h.deliver(p, sealed)
		h.setState(p.ID, s)
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
