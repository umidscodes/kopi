package app

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/recent"
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
	return startNamedNode(t, lan, id, id, key)
}

// startNamedNode is startNode for a device whose display name differs from its ID.
func startNamedNode(t *testing.T, lan *discovery.FakeLAN, id, name string, key *proto.Key) *testNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := clip.NewFake()
	tn := &testNode{id: id, clip: c, cancel: cancel}

	// The node's port is only known after Listen, so discovery is joined lazily.
	disc := &lazyJoin{lan: lan, id: id, name: name, ready: make(chan struct{})}
	n, err := Start(ctx, Options{
		DeviceID: id, DeviceName: name, Key: key, Clipboard: c,
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
	name  string
	port  int
	ready chan struct{}
}

func (l *lazyJoin) Run(ctx context.Context, emit func(discovery.Event)) error {
	<-l.ready
	self := transport.Peer{ID: l.id, Name: l.name,
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

func TestNodeAnswersHelloWithItsIdentity(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a-device-id", &keyAlpha)
	id, err := transport.Hello(context.Background(), netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(a.Port())).String())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "a-device-id" || id.Name != "a-device-id" {
		t.Fatalf("hello identity = %+v", id)
	}
	if id.Version != proto.Version {
		t.Fatalf("hello identity Version = %d, want proto.Version (%d)", id.Version, proto.Version)
	}
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

// --- Feature: Recent clips, per node ---

// firstRecent returns the newest entry of n's Recent clips list, if any.
func firstRecent(n *testNode) (recent.Entry, bool) {
	es := n.Recent.Entries()
	if len(es) == 0 {
		return recent.Entry{}, false
	}
	return es[0], true
}

func listsFirst(n *testNode, text, from string) func() bool {
	return func() bool {
		e, ok := firstRecent(n)
		return ok && e.Text == text && e.From == from
	}
}

func TestRECENT1_SentClipListedOnSender(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))

	a.clip.Copy("hello", false)
	eventually(t, time.Second, "A's first recent entry is hello from this device", listsFirst(a, "hello", "a"))
	eventually(t, time.Second, "B receives", func() bool { return b.clip.Text() == "hello" })
}

// The clipboard watcher is registered before Start returns, so a copy made right
// after start is sent, not lost (SYNC-1). No sleep between Start and Copy.
func TestSYNC1_CopyRightAfterStartIsSent(t *testing.T) {
	a := startNode(t, discovery.NewFakeLAN(), "a", &keyAlpha)
	a.clip.Copy("first thing", false)
	eventually(t, 2*time.Second, "A sent the copy made right after start", listsFirst(a, "first thing", "a"))
}

func TestRECENT2_ReceivedClipListedWithSenderID(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNamedNode(t, lan, "a-device-id", "Office PC", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	eventually(t, 2*time.Second, "B knows A's name", func() bool { return b.Recent.NameOf("a-device-id") == "Office PC" })

	a.clip.Copy("hello", false)
	eventually(t, time.Second, "B's first recent entry is hello from A", listsFirst(b, "hello", "a-device-id"))
	if e, _ := firstRecent(b); e.Name != "Office PC" {
		t.Fatalf("B's entry name = %q, want %q", e.Name, "Office PC")
	}
}

func TestRECENT9_SenderNameStoredWhenReceived(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNamedNode(t, lan, "a1b2c3d4e5", "Mac", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))
	eventually(t, 2*time.Second, "B knows A's name", func() bool { return b.Recent.NameOf("a1b2c3d4e5") == "Mac" })

	a.clip.Copy("from the mac", false)
	eventually(t, time.Second, "B lists A's clip", listsFirst(b, "from the mac", "a1b2c3d4e5"))

	a.stop()
	eventually(t, 2*time.Second, "B drops A", func() bool { return !knows(b, "a1b2c3d4e5")() })
	if got := b.Recent.NameOf("a1b2c3d4e5"); got != "" {
		t.Fatalf("NameOf after A left = %q, want empty", got)
	}
	// Decision 4: the name was stored when the clip was recorded, not looked up now.
	if e, _ := firstRecent(b); e.Name != "Mac" {
		t.Fatalf("entry name after A left = %q, want %q", e.Name, "Mac")
	}
}

func TestRECENT13_NewPassphraseEmptiesList(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	b := startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))

	a.clip.Copy("x", false)
	eventually(t, time.Second, "A lists x", listsFirst(a, "x", "a"))
	eventually(t, time.Second, "B lists x", listsFirst(b, "x", "a"))

	a.SetKey(&keyBravo)
	if es := a.Recent.Entries(); len(es) != 0 {
		t.Fatalf("A's list after new passphrase = %+v, want empty", es)
	}
	if !a.Engine.HasKey() {
		t.Fatal("A has no key after SetKey")
	}
	if s, _ := peerState(a, "b"); s != transport.StateUnknown {
		t.Fatalf("A sees B as %v after new passphrase, want unknown", s)
	}
	// Only this device's list is emptied.
	if _, ok := firstRecent(b); !ok {
		t.Fatal("B's list was emptied too")
	}
}

func TestRECENT15_PauseKeepsList(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	startNode(t, lan, "b", &keyAlpha)
	eventually(t, 2*time.Second, "A lists B", knows(a, "b"))

	a.clip.Copy("x", false)
	eventually(t, time.Second, "A lists x", listsFirst(a, "x", "a"))

	a.Engine.SetPaused(true)
	a.Engine.SetPaused(false)
	if es := a.Recent.Entries(); len(es) != 1 || es[0].Text != "x" {
		t.Fatalf("A's list after pause and resume = %+v, want [x]", es)
	}
}

// Reviewer note (PR #24): NameOf runs while the engine holds its lock. Recording clips
// while peers come, go and change state must neither deadlock nor race (run with
// -race in CI).
func TestRECENT9_NameLookupRacesPeerChanges(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)

	const rounds = 200
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // peers come, change name and state, and go
		defer wg.Done()
		addr := netip.MustParseAddrPort("127.0.0.1:1")
		for i := 0; i < rounds; i++ {
			a.Hub.Upsert(transport.Peer{ID: "p", Name: fmt.Sprintf("P%d", i), Addrs: []netip.AddrPort{addr}})
			a.Hub.MarkMismatchFrom(addr.Addr())
			a.Hub.ResetStates()
			if i%3 == 0 {
				a.Hub.Remove("p")
			}
		}
	}()
	go func() { // clips received from p, recorded under the engine lock
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			sealed, err := proto.Seal(keyAlpha, proto.Message{ID: proto.NewID(), From: "p",
				SentAt: time.Now().Add(time.Duration(i) * time.Millisecond), Text: fmt.Sprintf("in %d", i)})
			if err != nil {
				t.Error(err)
				return
			}
			_ = a.Engine.Receive(sealed)
		}
	}()
	go func() { // local copies, and the menu reading the list
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			a.Engine.HandleLocal(clip.Item{Text: fmt.Sprintf("out %d", i)})
			_ = a.Recent.Entries()
			_ = a.Hub.Peers()
		}
	}()
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: recording and peer changes did not finish")
	}
	for _, e := range a.Recent.Entries() {
		if e.From == "p" && e.Name != "" && !strings.HasPrefix(e.Name, "P") {
			t.Fatalf("entry from p has name %q", e.Name)
		}
	}
}
