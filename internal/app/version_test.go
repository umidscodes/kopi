package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net/netip"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

// sealV1 builds a frame the way v0.1 did: version byte 1 (authenticated), nonce, and
// id | sentAt ms | len(from) | from | text with no flags byte.
func sealV1(t *testing.T, key proto.Key, from, text string) []byte {
	t.Helper()
	id := proto.NewID()
	plain := append([]byte{}, id[:]...)
	plain = binary.BigEndian.AppendUint64(plain, uint64(time.Now().UnixMilli()))
	plain = append(plain, byte(len(from)))
	plain = append(plain, from...)
	plain = append(plain, text...)
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 1+aead.NonceSize())
	out[0] = 1
	if _, err := rand.Read(out[1:]); err != nil {
		t.Fatal(err)
	}
	return aead.Seal(out, out[1:], plain, out[:1])
}

func TestRECENT20_OldVersionFrameNotAppliedAndPeerMarked(t *testing.T) {
	lan := discovery.NewFakeLAN()
	a := startNode(t, lan, "a", &keyAlpha)
	a.clip.Copy("a-before", false)

	// B is a v0.1 device: it listens, and its discovery reports version 1.
	var (
		mu  sync.Mutex
		got int
	)
	srv, err := transport.Listen("127.0.0.1:0", func(netip.Addr, []byte) error {
		mu.Lock()
		got++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)
	go lan.Join(transport.Peer{ID: "old", Name: "old", Version: 1,
		Addrs: []netip.AddrPort{netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(srv.Port()))}}).Run(ctx, func(discovery.Event) {})
	eventually(t, 2*time.Second, "A lists old", knows(a, "old"))
	// C is a v0.1 device whose version discovery has not reported anything yet.
	go lan.Join(transport.Peer{ID: "quiet", Name: "quiet",
		Addrs: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")}}).Run(ctx, func(discovery.Event) {})
	eventually(t, 2*time.Second, "A lists quiet", knows(a, "quiet"))
	if s, _ := peerState(a, "quiet"); s != transport.StateUnknown {
		t.Fatalf("version-less peer before any old frame = %v, want unknown (found)", s)
	}

	// An old-format clip arrives over real TCP from 127.0.0.1 (same passphrase).
	addrA := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(a.Port())).String()
	transport.Send(context.Background(), addrA, sealV1(t, keyAlpha, "old", "from old"))

	eventually(t, 2*time.Second, "both old peers shown as needs update", func() bool {
		s1, _ := peerState(a, "old")
		s2, _ := peerState(a, "quiet")
		return s1 == transport.StateNeedsUpdate && s2 == transport.StateNeedsUpdate
	})
	if a.clip.Text() != "a-before" {
		t.Fatalf("A's clipboard changed to %q by an old-version clip", a.clip.Text())
	}

	// A copies: the old peer must receive nothing and stay "needs update".
	a.clip.Copy("to old", false)
	time.Sleep(300 * time.Millisecond)
	a.Hub.Wait()
	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Fatalf("v0.1 peer received %d frames, want 0", n)
	}
	for _, id := range []string{"old", "quiet"} {
		if s, _ := peerState(a, id); s != transport.StateNeedsUpdate {
			t.Fatalf("after A's copy, %s = %v, want needs update", id, s)
		}
	}
}
