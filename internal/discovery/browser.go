package discovery

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	gosync "sync"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

// ServiceType is Kopi's DNS-SD service type.
const ServiceType = "_kopi._tcp"

const (
	// BrowseInterval is how often the dnssd implementation asks the network who is
	// there. dnssd's browser queries only once at start and unsolicited announcements
	// are not reliably delivered, so it re-queries itself (DISC-1: ≤ 10 s).
	BrowseInterval = 5 * time.Second
	// PeerTimeout drops a peer that stopped answering, even if its goodbye packet was
	// lost (DISC-2: ≤ 30 s).
	PeerTimeout = 3 * BrowseInterval
)

func instanceName(self transport.Peer) string {
	// Instance names must be unique on the LAN; the ID suffix keeps two devices with
	// the same hostname apart.
	id := self.ID
	if len(id) > 6 {
		id = id[:6]
	}
	return self.Name + "-" + id
}

func txtRecord(self transport.Peer) map[string]string {
	return map[string]string{"id": self.ID, "name": self.Name, "v": strconv.Itoa(proto.Version)}
}

// versionFromTXT reads the protocol version a peer publishes in its TXT key "v".
// Missing, empty, non-numeric or non-positive gives 0: unknown, which the hub treats
// like our own version (normal states), never as "needs update" (RECENT-21).
func versionFromTXT(kv map[string]string) int {
	v, err := strconv.Atoi(kv["v"])
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

type seenPeer struct {
	peer     transport.Peer
	lastSeen time.Time
}

// browser turns raw sightings from either implementation into a stable peer list.
type browser struct {
	self    string
	emit    func(Event)
	now     func() time.Time
	timeout time.Duration // silence after which a peer is dropped

	mu    gosync.Mutex
	peers map[string]*seenPeer
}

func newBrowser(self string, emit func(Event)) *browser {
	return &browser{self: self, emit: emit, now: time.Now, timeout: PeerTimeout, peers: map[string]*seenPeer{}}
}

// seen records a sighting and emits Up when the peer is new, has new addresses or
// publishes a new protocol version. A sighting without a version (0) keeps the one
// we know.
func (b *browser) seen(p transport.Peer) {
	if p.ID == b.self {
		return
	}
	b.mu.Lock()
	cur, ok := b.peers[p.ID]
	changed := !ok
	if !ok {
		cur = &seenPeer{peer: transport.Peer{ID: p.ID, Name: p.Name}}
		b.peers[p.ID] = cur
	}
	if p.Version != 0 && p.Version != cur.peer.Version {
		cur.peer.Version = p.Version
		changed = true
	}
	for _, a := range p.Addrs {
		if !slices.Contains(cur.peer.Addrs, a) {
			cur.peer.Addrs = append(cur.peer.Addrs, a)
			changed = true
		}
	}
	cur.lastSeen = b.now()
	out := cur.peer
	out.Addrs = slices.Clone(cur.peer.Addrs)
	b.mu.Unlock()
	if changed {
		b.emit(Event{Up: true, Peer: out})
	}
}

func (b *browser) gone(id string) {
	b.mu.Lock()
	_, ok := b.peers[id]
	delete(b.peers, id)
	b.mu.Unlock()
	if ok {
		b.emit(Event{Up: false, Peer: transport.Peer{ID: id}})
	}
}

func (b *browser) expire() {
	now := b.now()
	var dead []string
	b.mu.Lock()
	for id, p := range b.peers {
		if now.Sub(p.lastSeen) > b.timeout {
			dead = append(dead, id)
		}
	}
	b.mu.Unlock()
	for _, id := range dead {
		b.gone(id)
	}
}

// usable filters out addresses a peer can't dial us on: loopback (only valid on the
// same machine) and link-local IPv6 (needs an interface zone).
func usable(ip net.IP) (netip.Addr, bool) {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if a.IsLoopback() || (a.Is6() && a.IsLinkLocalUnicast()) {
		return netip.Addr{}, false
	}
	return a, true
}
