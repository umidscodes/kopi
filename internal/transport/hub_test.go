package transport_test

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

func hubStates(h *transport.Hub) map[string]transport.State {
	states := map[string]transport.State{}
	for _, p := range h.Peers() {
		states[p.ID] = p.State
	}
	return states
}

func TestRECENT21_OldVersionPeerShownAsNeedsUpdate(t *testing.T) {
	if got := transport.StateNeedsUpdate.String(); got != "needs update" {
		t.Fatalf("StateNeedsUpdate.String() = %q, want %q", got, "needs update")
	}
	h := transport.NewHub()
	addr := []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:47800")}
	h.Upsert(transport.Peer{ID: "old", Name: "Old", Addrs: addr, Version: 1})
	h.Upsert(transport.Peer{ID: "future", Name: "Future", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.3:47800")}, Version: proto.Version + 1})
	h.Upsert(transport.Peer{ID: "same", Name: "Same", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.2:47800")}, Version: proto.Version})

	s := hubStates(h)
	if s["old"] != transport.StateNeedsUpdate || s["future"] != transport.StateNeedsUpdate {
		t.Fatalf("old=%v future=%v, want both needs update", s["old"], s["future"])
	}
	if s["same"] != transport.StateUnknown {
		t.Fatalf("same-version peer = %v, want unknown (found)", s["same"])
	}

	// A later sighting without a version keeps the one we know.
	h.Upsert(transport.Peer{ID: "old", Addrs: addr})
	for _, p := range h.Peers() {
		if p.ID == "old" && (p.Version != 1 || p.State != transport.StateNeedsUpdate) {
			t.Fatalf("after a version-less sighting: Version=%d State=%v, want 1, needs update", p.Version, p.State)
		}
	}
	// Latest non-zero version wins: the peer updated.
	h.Upsert(transport.Peer{ID: "old", Version: proto.Version})
	if s := hubStates(h)["old"]; s == transport.StateNeedsUpdate {
		t.Fatalf("peer reporting our version still shown as %v", s)
	}
}

func TestRECENT21_NeedsUpdateSurvivesMismatchAndReset(t *testing.T) {
	ip := netip.MustParseAddr("10.0.0.1")
	h := transport.NewHub()
	h.Upsert(transport.Peer{ID: "old", Name: "Old", Addrs: []netip.AddrPort{netip.AddrPortFrom(ip, 47800)}, Version: 1})
	// A version-less peer whose old frame arrived (receive path).
	ip2 := netip.MustParseAddr("10.0.0.2")
	h.Upsert(transport.Peer{ID: "quiet", Name: "Quiet", Addrs: []netip.AddrPort{netip.AddrPortFrom(ip2, 47800)}})
	h.MarkNeedsUpdateFrom(ip2)

	check := func(when string) {
		t.Helper()
		s := hubStates(h)
		if s["old"] != transport.StateNeedsUpdate || s["quiet"] != transport.StateNeedsUpdate {
			t.Fatalf("%s: old=%v quiet=%v, want both needs update", when, s["old"], s["quiet"])
		}
	}
	check("initially")
	h.MarkMismatchFrom(ip)
	h.MarkMismatchFrom(ip2)
	check("after MarkMismatchFrom")
	h.ResetStates()
	check("after ResetStates")
	h.Upsert(transport.Peer{ID: "quiet", Name: "Quiet"})
	check("after a later version-less sighting")
}

func TestRECENT20_NoClipSentToOldVersionPeer(t *testing.T) {
	var (
		mu     sync.Mutex
		dialed []string
	)
	h := transport.NewHub()
	h.RetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	h.Send = func(_ context.Context, addr string, _ []byte) error {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		return errors.New("network down") // would make any peer unreachable
	}
	h.Upsert(transport.Peer{ID: "old", Name: "Old", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:47800")}, Version: 1})
	ip2 := netip.MustParseAddr("10.0.0.2")
	h.Upsert(transport.Peer{ID: "quiet", Name: "Quiet", Addrs: []netip.AddrPort{netip.AddrPortFrom(ip2, 47800)}})
	h.MarkNeedsUpdateFrom(ip2)

	h.Broadcast([]byte("to old"))
	h.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 0 {
		t.Fatalf("Send called for %v; a needs-update peer must never be sent a clip", dialed)
	}
	s := hubStates(h)
	if s["old"] != transport.StateNeedsUpdate || s["quiet"] != transport.StateNeedsUpdate {
		t.Fatalf("old=%v quiet=%v, want both needs update", s["old"], s["quiet"])
	}
}

func TestRECENT21_UnknownVersionPeerUsesNormalStates(t *testing.T) {
	h := transport.NewHub()
	h.Send = func(context.Context, string, []byte) error { return nil }
	h.Upsert(transport.Peer{ID: "p", Name: "P", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:47800")}})
	if s := hubStates(h)["p"]; s != transport.StateUnknown {
		t.Fatalf("version-less peer = %v, want unknown (found)", s)
	}
	h.Broadcast([]byte("x"))
	h.Wait()
	if s := hubStates(h)["p"]; s != transport.StateOK {
		t.Fatalf("version-less peer after a send = %v, want ok (connected)", s)
	}
	h.MarkMismatchFrom(netip.MustParseAddr("10.0.0.1"))
	if s := hubStates(h)["p"]; s != transport.StateMismatch {
		t.Fatalf("version-less peer after decrypt failure = %v, want mismatch", s)
	}
}
