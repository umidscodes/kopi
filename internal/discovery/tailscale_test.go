package discovery

import (
	"context"
	"errors"
	"net/netip"
	gosync "sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

// Trimmed real output of `tailscale status --json` (Windows, 2026-10-18).
const sampleStatus = `{
  "Self": {"HostName": "Goonputer", "TailscaleIPs": ["100.124.98.3", "fd7a:115c:a1e0::8c34:6204"], "Online": true},
  "Peer": {
    "nodekey:aa": {"HostName": "Umut’s MacBook Pro", "TailscaleIPs": ["100.125.24.127", "fd7a:115c:a1e0::2234:1881"], "Online": true, "OS": "macOS"},
    "nodekey:bb": {"HostName": "gx10-6b9b", "TailscaleIPs": ["fd7a:115c:a1e0::2901:6dd5", "100.109.109.100"], "Online": true, "OS": "linux"},
    "nodekey:cc": {"HostName": "old-phone", "TailscaleIPs": ["100.64.0.9"], "Online": false}
  }
}`

func TestParseTailscaleStatusKeepsOnlineIPv4Peers(t *testing.T) {
	devs, err := parseTailscaleStatus([]byte(sampleStatus))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range devs {
		got[d.Name] = d.IP.String()
	}
	want := map[string]string{"Umut’s MacBook Pro": "100.125.24.127", "gx10-6b9b": "100.109.109.100"}
	if len(got) != len(want) {
		t.Fatalf("devices = %v, want %v (self and offline excluded)", got, want)
	}
	for n, ip := range want {
		if got[n] != ip {
			t.Errorf("%s: got %q, want %q", n, got[n], ip)
		}
	}
}

func TestParseTailscaleStatusRejectsGarbage(t *testing.T) {
	if _, err := parseTailscaleStatus([]byte("not json")); err == nil {
		t.Fatal("garbage parsed without error")
	}
}

type eventLog struct {
	mu     gosync.Mutex
	events []Event
}

func (l *eventLog) emit(ev Event) { l.mu.Lock(); l.events = append(l.events, ev); l.mu.Unlock() }
func (l *eventLog) snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Event(nil), l.events...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestDISC4_TailnetPeerThatAnswersHelloIsFound(t *testing.T) {
	mac := netip.MustParseAddr("100.125.24.127")
	gx10 := netip.MustParseAddr("100.109.109.100")
	ts := Tailscale{
		Self: transport.Peer{ID: "pc"},
		Port: 47800,
		Status: func(context.Context) ([]TailnetDevice, error) {
			return []TailnetDevice{{Name: "mac", IP: mac}, {Name: "gx10", IP: gx10}}, nil
		},
		Hello: func(_ context.Context, addr string) (transport.Identity, error) {
			if addr == "100.125.24.127:47800" {
				return transport.Identity{ID: "mac-id", Name: "MacBook", Version: 1}, nil
			}
			return transport.Identity{}, errors.New("connection refused") // DISC-6: not Kopi
		},
	}
	var log eventLog
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx, log.emit)
	waitFor(t, "Up for the Mac", func() bool { return len(log.snapshot()) > 0 })
	time.Sleep(50 * time.Millisecond)
	evs := log.snapshot()
	if len(evs) != 1 || !evs[0].Up || evs[0].Peer.ID != "mac-id" || evs[0].Peer.Name != "MacBook" {
		t.Fatalf("events = %+v, want one Up for mac-id only", evs)
	}
	if a := evs[0].Peer.Addrs; len(a) != 1 || a[0] != netip.AddrPortFrom(mac, 47800) {
		t.Fatalf("addrs = %v, want the Mac's tailnet address", a)
	}
}

func TestTailnetIgnoresItself(t *testing.T) {
	ts := Tailscale{
		Self: transport.Peer{ID: "pc"},
		Port: 47800,
		Status: func(context.Context) ([]TailnetDevice, error) {
			return []TailnetDevice{{Name: "pc-again", IP: netip.MustParseAddr("100.64.0.1")}}, nil
		},
		Hello: func(context.Context, string) (transport.Identity, error) {
			return transport.Identity{ID: "pc"}, nil // e.g. a second route back to this device
		},
	}
	var log eventLog
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ts.Run(ctx, log.emit)
	if evs := log.snapshot(); len(evs) != 0 {
		t.Fatalf("listed itself: %+v", evs)
	}
}

// RECENT-21 tailnet path: the peer's protocol version comes from the hello answer.
func TestRECENT21_TailnetPeerVersionReported(t *testing.T) {
	mac := netip.MustParseAddr("100.125.24.127")
	ts := Tailscale{
		Self: transport.Peer{ID: "pc"},
		Port: 47800,
		Status: func(context.Context) ([]TailnetDevice, error) {
			return []TailnetDevice{{Name: "mac", IP: mac}}, nil
		},
		Hello: func(context.Context, string) (transport.Identity, error) {
			return transport.Identity{ID: "mac-id", Name: "MacBook", Version: 1}, nil
		},
	}
	var log eventLog
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx, log.emit)
	waitFor(t, "Up for the Mac", func() bool { return len(log.snapshot()) > 0 })
	evs := log.snapshot()
	if len(evs) != 1 || evs[0].Peer.ID != "mac-id" || evs[0].Peer.Version != 1 {
		t.Fatalf("events = %+v, want one Up for mac-id with Version 1", evs)
	}
}

// Decision 3 on #15: a hello answer with v missing or not positive is reported as
// Version 0 (unknown = normal states), never as "needs update".
func TestRECENT21_TailnetPeerWithoutVersionIsUnknown(t *testing.T) {
	ipA := netip.MustParseAddr("100.64.0.1")
	ipB := netip.MustParseAddr("100.64.0.2")
	ts := Tailscale{
		Self: transport.Peer{ID: "pc"},
		Port: 47800,
		Status: func(context.Context) ([]TailnetDevice, error) {
			return []TailnetDevice{{Name: "no-v", IP: ipA}, {Name: "neg-v", IP: ipB}}, nil
		},
		Hello: func(_ context.Context, addr string) (transport.Identity, error) {
			if addr == "100.64.0.1:47800" {
				return transport.Identity{ID: "no-v-id", Name: "NoV"}, nil // v missing
			}
			return transport.Identity{ID: "neg-v-id", Name: "NegV", Version: -1}, nil // unreadable
		},
	}
	var log eventLog
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx, log.emit)
	waitFor(t, "both peers listed", func() bool {
		ids := map[string]bool{}
		for _, ev := range log.snapshot() {
			ids[ev.Peer.ID] = true
		}
		return ids["no-v-id"] && ids["neg-v-id"]
	})
	for _, ev := range log.snapshot() {
		if ev.Peer.Version != 0 {
			t.Fatalf("peer %s reported Version %d, want 0 (unknown)", ev.Peer.ID, ev.Peer.Version)
		}
	}
}

// Reviewer note on #15 (PR #21 review): two nodes running the current version must
// never show each other as "needs update" over the tailnet path, so the version a
// node answers with hello must reach the hub intact.
func TestRECENT21_TailnetCurrentVersionNeverNeedsUpdate(t *testing.T) {
	other := netip.MustParseAddr("100.64.0.9")
	hub := transport.NewHub()
	ts := Tailscale{
		Self: transport.Peer{ID: "me"},
		Port: 47800,
		Status: func(context.Context) ([]TailnetDevice, error) {
			return []TailnetDevice{{Name: "other", IP: other}}, nil
		},
		Hello: func(context.Context, string) (transport.Identity, error) {
			return transport.Identity{ID: "other-id", Name: "Other", Version: proto.Version}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx, func(ev Event) {
		if ev.Up {
			hub.Upsert(ev.Peer)
		}
	})
	waitFor(t, "the peer is listed", func() bool { return len(hub.Peers()) == 1 })
	p := hub.Peers()[0]
	if p.Version != proto.Version || p.State == transport.StateNeedsUpdate {
		t.Fatalf("peer = %+v state %v, want Version %d and not needs update", p, p.State, proto.Version)
	}
}

// fakeSource is a Discovery driven by the test through a channel.
type fakeSource struct{ events chan Event }

func (f fakeSource) Run(ctx context.Context, emit func(Event)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-f.events:
			emit(ev)
		}
	}
}

func TestDISC5_MultiListsPeerOnceAndDropsItOnlyWhenAllSourcesDo(t *testing.T) {
	mdns, tail := fakeSource{make(chan Event)}, fakeSource{make(chan Event)}
	var log eventLog
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Multi{mdns, tail}.Run(ctx, log.emit)

	b := transport.Peer{ID: "b", Name: "B"}
	mdns.events <- Event{Up: true, Peer: b}
	tail.events <- Event{Up: true, Peer: b}
	// Sources run in their own goroutines; make sure both Ups were processed before
	// sending the Down, or the test races its own setup.
	waitFor(t, "both Ups processed", func() bool { return len(log.snapshot()) == 2 })
	mdns.events <- Event{Up: false, Peer: transport.Peer{ID: "b"}} // LAN lost, tailnet still sees it
	time.Sleep(50 * time.Millisecond)
	for _, ev := range log.snapshot() {
		if !ev.Up {
			t.Fatalf("Down emitted while the tailnet still sees b: %+v", log.snapshot())
		}
	}
	tail.events <- Event{Up: false, Peer: transport.Peer{ID: "b"}}
	waitFor(t, "Down once both sources lost b", func() bool {
		evs := log.snapshot()
		return len(evs) > 0 && !evs[len(evs)-1].Up
	})
	// A Down from a source that never reported b must not emit anything.
	mdns.events <- Event{Up: false, Peer: transport.Peer{ID: "ghost"}}
	time.Sleep(50 * time.Millisecond)
	if evs := log.snapshot(); evs[len(evs)-1].Peer.ID == "ghost" {
		t.Fatal("Down emitted for a peer no source had reported")
	}
}
