package app

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

var (
	keyAlpha = proto.DeriveKey("alpha")
	keyBravo = proto.DeriveKey("bravo")
)

type testNode struct {
	*Node
	id     string
	clip   *clip.Fake
	cancel context.CancelFunc
}

// startNode brings up a real node on 127.0.0.1 (never 0.0.0.0, so tests don't trip
// the Windows firewall prompt) and joins it to the fake LAN.
func startNode(t *testing.T, lan *discovery.FakeLAN, id string, key *proto.Key) *testNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := clip.NewFake()
	tn := &testNode{id: id, clip: c, cancel: cancel}

	// The node's port is only known after Listen, so discovery is joined lazily.
	disc := &lazyJoin{lan: lan, id: id, ready: make(chan struct{})}
	n, err := Start(ctx, Options{
		DeviceID: id, DeviceName: id, Key: key, Clipboard: c,
		ListenAddr: "127.0.0.1:0", Discovery: disc,
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	disc.port = n.Port()
	close(disc.ready)
	tn.Node = n
	t.Cleanup(func() { cancel(); n.Wait() })
	return tn
}

// stop shuts the node down and waits until it is fully gone.
func (tn *testNode) stop() {
	tn.cancel()
	tn.Wait()
}

type lazyJoin struct {
	lan   *discovery.FakeLAN
	id    string
	port  int
	ready chan struct{}
}

func (l *lazyJoin) Run(ctx context.Context, emit func(discovery.Event)) error {
	<-l.ready
	self := transport.Peer{ID: l.id, Name: l.id,
		Addrs: []netip.AddrPort{netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(l.port))}}
	return l.lan.Join(self).Run(ctx, emit)
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for: %s", timeout, what)
}

func peerState(n *testNode, id string) (transport.State, bool) {
	for _, p := range n.Hub.Peers() {
		if p.ID == id {
			return p.State, true
		}
	}
	return 0, false
}

func knows(n *testNode, id string) func() bool {
	return func() bool { _, ok := peerState(n, id); return ok }
}

func TestDISC1_PeersFindEachOther(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	eventually(t, 2*time.Second, "B lists A", knows(b, "a"))
}

func TestSYNC_OverRealTCP_ThreePeers(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	c := startNode(t, lan, "c", &keyAlpha)
	eventually(t, 2*time.Second, "everyone knows everyone", func() bool {
		return len(a.Hub.Peers()) == 2 && len(b.Hub.Peers()) == 2 && len(c.Hub.Peers()) == 2
	})

	a.clip.Copy("over the wire 🌍", false)
	eventually(t, time.Second, "B and C receive within 1s (SYNC-1, SYNC-3)", func() bool {
		return b.clip.Text() == "over the wire 🌍" && c.clip.Text() == "over the wire 🌍"
	})

	c.clip.Copy("back from C", false)
	eventually(t, time.Second, "A and B receive C's clip (SYNC-2)", func() bool {
		return a.clip.Text() == "back from C" && b.clip.Text() == "back from C"
	})

	time.Sleep(150 * time.Millisecond)
	// SYNC-4 over the network: each clip is applied exactly once per receiver.
	if a.clip.Writes() != 1 || b.clip.Writes() != 2 || c.clip.Writes() != 1 {
		t.Fatalf("writes A=%d B=%d C=%d, want 1, 2, 1", a.clip.Writes(), b.clip.Writes(), c.clip.Writes())
	}
	if s, _ := peerState(a, "b"); s != transport.StateOK {
		t.Fatalf("A sees B as %v, want ok", s)
	}
}

func TestSEC1_MismatchShownOnBothSides(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyBravo)
	eventually(t, 2*time.Second, "A and B discover each other", func() bool { return knows(a, "b")() && knows(b, "a")() })

	a.clip.Copy("secret", false)
	eventually(t, 2*time.Second, "A marks B as mismatch", func() bool {
		s, _ := peerState(a, "b")
		return s == transport.StateMismatch
	})
	eventually(t, 2*time.Second, "B marks A as mismatch", func() bool {
		s, _ := peerState(b, "a")
		return s == transport.StateMismatch
	})
	if b.clip.Text() != "" {
		t.Fatalf("B's clipboard changed to %q", b.clip.Text())
	}
}

func TestDISC2_PeerThatQuitsIsRemoved(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	b.stop()
	eventually(t, 2*time.Second, "A drops B", func() bool { return !knows(a, "b")() })
}

func TestDISC3_RestartedPeerIsFoundAndSyncs(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	b.stop()
	eventually(t, 2*time.Second, "A drops B", func() bool { return !knows(a, "b")() })

	b2 := startNode(t, lan, "b", &keyAlpha) // same device ID, new port
	eventually(t, 2*time.Second, "A lists B again", knows(a, "b"))
	a.clip.Copy("welcome back", false)
	eventually(t, time.Second, "restarted B receives", func() bool { return b2.clip.Text() == "welcome back" })
}

func TestDeadPeerDoesNotBlockOthers(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	// A ghost peer that is listed but not listening.
	a.Hub.Upsert(transport.Peer{ID: "ghost", Name: "ghost",
		Addrs: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")}})

	a.clip.Copy("still works", false)
	eventually(t, time.Second, "B receives despite the ghost", func() bool { return b.clip.Text() == "still works" })
	eventually(t, 5*time.Second, "ghost marked unreachable", func() bool {
		s, _ := peerState(a, "ghost")
		return s == transport.StateUnreachable
	})
}
