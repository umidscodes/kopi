package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	gosync "sync"
	"time"

	"github.com/brutella/dnssd"

	"github.com/umidscodes/kopi/internal/transport"
)

// ServiceType is Kopi's DNS-SD service type.
const ServiceType = "_kopi._tcp"

const (
	// BrowseInterval is how often we ask the network who is there. dnssd's browser
	// queries only once at start and unsolicited announcements are not reliably
	// delivered (verified on macOS), so we re-query ourselves (DISC-1: ≤ 10 s).
	BrowseInterval = 5 * time.Second
	// PeerTimeout drops a peer that stopped answering, even if its goodbye packet was
	// lost (DISC-2: ≤ 30 s).
	PeerTimeout = 3 * BrowseInterval
)

// MDNS announces this device with mDNS/DNS-SD and browses for others.
// Only device ID, name and protocol version are published; nothing secret.
type MDNS struct {
	Self transport.Peer // ID and Name; Addrs are ignored
	Port int
}

func (m MDNS) Run(ctx context.Context, emit func(Event)) error {
	cfg := dnssd.Config{
		// Instance names must be unique on the LAN; the ID suffix keeps two devices
		// with the same hostname apart.
		Name: fmt.Sprintf("%s-%.6s", m.Self.Name, m.Self.ID),
		Type: ServiceType,
		Port: m.Port,
		Text: map[string]string{"id": m.Self.ID, "name": m.Self.Name, "v": "1"},
	}
	svc, err := dnssd.NewService(cfg)
	if err != nil {
		return fmt.Errorf("mdns service: %w", err)
	}
	rp, err := dnssd.NewResponder()
	if err != nil {
		return fmt.Errorf("mdns responder: %w", err)
	}
	if _, err := rp.Add(svc); err != nil {
		return fmt.Errorf("mdns add: %w", err)
	}
	respondErr := make(chan error, 1)
	// Respond sends goodbye packets when ctx is cancelled, so peers drop us at once.
	go func() { respondErr <- rp.Respond(ctx) }()

	b := browser{self: m.Self.ID, emit: emit, now: time.Now, peers: map[string]*seenPeer{}}
	t := time.NewTicker(BrowseInterval)
	defer t.Stop()
	for {
		b.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case err := <-respondErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-t.C:
		}
	}
}

type seenPeer struct {
	peer     transport.Peer
	lastSeen time.Time
}

// browser turns repeated one-shot browses into a stable peer list.
type browser struct {
	self string
	emit func(Event)
	now  func() time.Time

	mu    gosync.Mutex
	peers map[string]*seenPeer
}

// round runs one browse of slightly less than BrowseInterval, then expires peers
// that haven't answered for PeerTimeout.
func (b *browser) round(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, BrowseInterval-500*time.Millisecond)
	defer cancel()
	dnssd.LookupType(rctx, ServiceType+".local.",
		func(e dnssd.BrowseEntry) {
			if p, ok := peerFrom(e); ok {
				b.seen(p)
			}
		},
		func(e dnssd.BrowseEntry) {
			if id := e.Text["id"]; id != "" {
				b.gone(id) // goodbye packet: drop at once
			}
		})
	b.expire()
}

// seen records a sighting and emits Up when the peer is new or has new addresses.
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
		if now.Sub(p.lastSeen) > PeerTimeout {
			dead = append(dead, id)
		}
	}
	b.mu.Unlock()
	for _, id := range dead {
		b.gone(id)
	}
}

func peerFrom(e dnssd.BrowseEntry) (transport.Peer, bool) {
	id := e.Text["id"]
	if id == "" || e.Port <= 0 || e.Port > 65535 {
		return transport.Peer{}, false
	}
	p := transport.Peer{ID: id, Name: e.Text["name"]}
	for _, ip := range e.IPs {
		if a, ok := usable(ip); ok {
			p.Addrs = append(p.Addrs, netip.AddrPortFrom(a, uint16(e.Port)))
		}
	}
	if len(p.Addrs) == 0 {
		// Fall back to resolving the advertised host name.
		if ips, err := net.LookupIP(e.Host); err == nil {
			for _, ip := range ips {
				if a, ok := usable(ip); ok {
					p.Addrs = append(p.Addrs, netip.AddrPortFrom(a, uint16(e.Port)))
				}
			}
		}
	}
	return p, len(p.Addrs) > 0
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
