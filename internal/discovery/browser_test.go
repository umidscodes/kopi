package discovery

import (
	"net/netip"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/transport"
)

type recorder struct{ events []Event }

func (r *recorder) emit(ev Event) { r.events = append(r.events, ev) }

func newTestBrowser(now *time.Time) (*browser, *recorder) {
	r := &recorder{}
	return &browser{self: "me", emit: r.emit, now: func() time.Time { return *now }, peers: map[string]*seenPeer{}}, r
}

func peer(id string, addrs ...string) transport.Peer {
	p := transport.Peer{ID: id, Name: id}
	for _, a := range addrs {
		p.Addrs = append(p.Addrs, netip.MustParseAddrPort(a))
	}
	return p
}

func TestBrowserEmitsUpOnceForStablePeer(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)
	b.seen(peer("a", "10.0.0.2:47800"))
	b.seen(peer("a", "10.0.0.2:47800"))
	if len(r.events) != 1 || !r.events[0].Up {
		t.Fatalf("events = %+v, want exactly one Up", r.events)
	}
}

func TestBrowserEmitsUpAgainForNewAddress(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)
	b.seen(peer("a", "10.0.0.2:47800"))
	b.seen(peer("a", "100.64.0.2:47800"))
	if len(r.events) != 2 || len(r.events[1].Peer.Addrs) != 2 {
		t.Fatalf("events = %+v, want a second Up carrying both addresses", r.events)
	}
}

func TestBrowserIgnoresSelf(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)
	b.seen(peer("me", "10.0.0.1:47800"))
	if len(r.events) != 0 {
		t.Fatalf("saw itself: %+v", r.events)
	}
}

func TestDISC2_SilentPeerExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)
	b.seen(peer("a", "10.0.0.2:47800"))
	now = now.Add(PeerTimeout)
	b.expire()
	if len(r.events) != 1 {
		t.Fatalf("expired too early: %+v", r.events)
	}
	now = now.Add(time.Second)
	b.expire()
	if len(r.events) != 2 || r.events[1].Up || r.events[1].Peer.ID != "a" {
		t.Fatalf("events = %+v, want Down for a", r.events)
	}
	if PeerTimeout+BrowseInterval > 30*time.Second {
		t.Fatalf("worst-case removal %v exceeds DISC-2's 30 s", PeerTimeout+BrowseInterval)
	}
}

func TestDISC3_ExpiredPeerComesBack(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)
	b.seen(peer("a", "10.0.0.2:47800"))
	b.gone("a")
	b.seen(peer("a", "10.0.0.2:47801")) // restarted on a new port
	if len(r.events) != 3 || !r.events[2].Up {
		t.Fatalf("events = %+v, want Up, Down, Up", r.events)
	}
}

func TestUsableFiltersLoopbackAndLinkLocal(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": false, "::1": false, "fe80::1": false,
		"192.168.2.86": true, "100.125.24.127": true, "fd44::1": true,
	} {
		ip := netip.MustParseAddr(addr).AsSlice()
		if _, ok := usable(ip); ok != want {
			t.Errorf("usable(%s) = %v, want %v", addr, ok, want)
		}
	}
}
