package discovery

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

func TestRECENT21_TXTRecordAdvertisesVersion2(t *testing.T) {
	kv := txtRecord(transport.Peer{ID: "abcdef123", Name: "pc"})
	if want := strconv.Itoa(proto.Version); kv["v"] != want {
		t.Fatalf("txt v = %q, want %q (proto.Version)", kv["v"], want)
	}
	if proto.Version != 2 {
		t.Fatalf("proto.Version = %d, want 2", proto.Version)
	}
	if kv["id"] != "abcdef123" || kv["name"] != "pc" {
		t.Fatalf("txt = %v, want id and name kept", kv)
	}
}

func TestRECENT21_VersionFromTXT(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]string
		want int
	}{
		{"v1", map[string]string{"v": "1"}, 1},
		{"v2", map[string]string{"v": "2"}, 2},
		{"v3", map[string]string{"v": "3"}, 3},
		{"missing", map[string]string{"id": "x"}, 0},
		{"nil map", nil, 0},
		{"empty", map[string]string{"v": ""}, 0},
		{"non-numeric", map[string]string{"v": "abc"}, 0},
		{"negative", map[string]string{"v": "-1"}, 0},
		{"zero", map[string]string{"v": "0"}, 0},
	}
	for _, c := range cases {
		if got := versionFromTXT(c.kv); got != c.want {
			t.Errorf("%s: versionFromTXT(%v) = %d, want %d", c.name, c.kv, got, c.want)
		}
	}
}

func TestRECENT21_BrowserPassesPeerVersion(t *testing.T) {
	now := time.Unix(1000, 0)
	b, r := newTestBrowser(&now)

	p := peer("a", "10.0.0.2:47800")
	p.Version = 1
	b.seen(p)
	if len(r.events) != 1 || r.events[0].Peer.Version != 1 {
		t.Fatalf("events = %+v, want one Up with Version 1", r.events)
	}

	// Same sighting again: nothing new.
	b.seen(p)
	if len(r.events) != 1 {
		t.Fatalf("events = %+v, want no second Up for an unchanged peer", r.events)
	}

	// A sighting without a readable version keeps the one we know and adds nothing.
	b.seen(peer("a", "10.0.0.2:47800"))
	if len(r.events) != 1 {
		t.Fatalf("events = %+v, want no Up for a version-less sighting", r.events)
	}

	// The peer updated: Up again so the hub learns the new version.
	p.Version = proto.Version
	b.seen(p)
	if len(r.events) != 2 || !r.events[1].Up || r.events[1].Peer.Version != proto.Version {
		t.Fatalf("events = %+v, want a second Up with Version %d", r.events, proto.Version)
	}
	if len(r.events[1].Peer.Addrs) != 1 {
		t.Fatalf("addresses lost on version change: %+v", r.events[1].Peer)
	}
}

// Reviewer note on #12: two devices running the current build must never show each
// other as "needs update". Each one's published TXT record is read by the other
// through the same helpers discovery uses, and the hub must keep normal states.
func TestRECENT21_CurrentVersionNodesNeverNeedUpdate(t *testing.T) {
	nodes := map[string]transport.Peer{
		"aaaaaa1": {ID: "aaaaaa1", Name: "A", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:47800")}},
		"bbbbbb2": {ID: "bbbbbb2", Name: "B", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.2:47800")}},
	}
	for me, other := range map[string]string{"aaaaaa1": "bbbbbb2", "bbbbbb2": "aaaaaa1"} {
		hub := transport.NewHub()
		b := newBrowser(me, func(ev Event) {
			if ev.Up {
				hub.Upsert(ev.Peer)
			}
		})
		kv := txtRecord(nodes[other]) // what the other device publishes
		b.seen(transport.Peer{ID: kv["id"], Name: kv["name"], Addrs: nodes[other].Addrs, Version: versionFromTXT(kv)})

		ps := hub.Peers()
		if len(ps) != 1 || ps[0].ID != other {
			t.Fatalf("%s: hub peers = %+v, want only %s", me, ps, other)
		}
		if ps[0].State == transport.StateNeedsUpdate || ps[0].Version != proto.Version {
			t.Fatalf("%s sees %s as %v (Version %d), want a normal state and Version %d",
				me, other, ps[0].State, ps[0].Version, proto.Version)
		}
	}
}
