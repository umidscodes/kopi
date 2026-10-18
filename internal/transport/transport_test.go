package transport_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/transport"
)

// startServer runs a Server on 127.0.0.1:0 until the test ends and returns its address.
func startServer(t *testing.T, h transport.Handler) string {
	t.Helper()
	s, err := transport.Listen("127.0.0.1:0", h)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx)
	t.Cleanup(cancel)
	return s.Addr().String()
}

// recorder collects the sealed bytes each handler call receives.
type recorder struct {
	mu   sync.Mutex
	seen [][]byte
}

// handler records every message and returns retErr to the server.
func (r *recorder) handler(retErr error) transport.Handler {
	return func(_ netip.Addr, sealed []byte) error {
		r.mu.Lock()
		r.seen = append(r.seen, bytes.Clone(sealed))
		r.mu.Unlock()
		return retErr
	}
}

func (r *recorder) payloads() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.seen))
	copy(out, r.seen)
	return out
}

func TestSendHandlerOKReturnsNilAndExactBytes(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil))
	sealed := []byte("sealed-payload-1")
	if err := transport.Send(context.Background(), addr, sealed); err != nil {
		t.Fatalf("Send with a happy handler returned %v, want nil", err)
	}
	got := r.payloads()
	if len(got) != 1 {
		t.Fatalf("handler saw %d messages, want exactly 1", len(got))
	}
	if !bytes.Equal(got[0], sealed) {
		t.Fatalf("handler saw %q, want %q", got[0], sealed)
	}
}

func TestSendDecryptErrorMapsToPeerMismatch(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(fmt.Errorf("proto.Open: %w", proto.ErrDecrypt)))
	err := transport.Send(context.Background(), addr, []byte("sealed-with-other-key"))
	if !errors.Is(err, transport.ErrPeerMismatch) {
		t.Fatalf("Send error = %v, want ErrPeerMismatch", err)
	}
	if errors.Is(err, transport.ErrRejected) {
		t.Fatalf("Send error %v also matches ErrRejected; the acks must stay distinct", err)
	}
}

func TestSendOtherHandlerErrorMapsToRejected(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(errors.New("replay detected")))
	err := transport.Send(context.Background(), addr, []byte("sealed"))
	if !errors.Is(err, transport.ErrRejected) {
		t.Fatalf("Send error = %v, want ErrRejected", err)
	}
	if errors.Is(err, transport.ErrPeerMismatch) {
		t.Fatalf("Send error %v also matches ErrPeerMismatch; only ErrDecrypt means mismatch", err)
	}
}

func TestOversizedFrameIsRejectedWithoutCallingHandler(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Announce one byte more than allowed and send nothing else: the server
	// must reject on the header alone, never read (or allocate) the body.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(proto.MaxFrameBytes)+1)
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("Write header: %v", err)
	}

	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		t.Fatalf("reading ack after oversized header: %v", err)
	}
	if ack[0] != 2 {
		t.Fatalf("ack = %d, want 2 (rejected)", ack[0])
	}
	// The server must close after the ack.
	if n, err := conn.Read(ack[:]); err != io.EOF || n != 0 {
		t.Fatalf("after ack, Read = (%d, %v), want EOF from a closed connection", n, err)
	}
	if got := r.payloads(); len(got) != 0 {
		t.Fatalf("handler was called with %d payloads for an oversized frame", len(got))
	}
}

func TestGarbageDoesNotReachHandlerAndServerSurvives(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil))
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0xff, 0xfe, 0xfd}); err != nil {
		t.Fatalf("Write garbage: %v", err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	// A truncated frame must not kill the server: a normal Send right after
	// still works end to end.
	if err := transport.Send(context.Background(), addr, []byte("ping")); err != nil {
		t.Fatalf("Send after garbage: %v", err)
	}
	for _, p := range r.payloads() {
		if !bytes.Equal(p, []byte("ping")) {
			t.Fatalf("handler saw %q; garbage must never reach it", p)
		}
	}
}

func TestSilentClientDoesNotBlockOtherSenders(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil))
	silent, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer silent.Close() // opens the connection, sends nothing, stays quiet

	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{err: transport.Send(context.Background(), addr, []byte("noisy"))}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Send beside a silent client returned %v, want nil", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not finish within 2s; a silent client blocked the server")
	}
}

func TestConcurrentSendersSucceedOrFailAtNetworkLevel(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil))
	const n = 20
	sent := make([][]byte, n)
	for i := range sent {
		sent[i] = []byte(fmt.Sprintf("payload-%02d", i))
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = transport.Send(context.Background(), addr, sent[i])
		}(i)
	}
	wg.Wait()

	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, transport.ErrPeerMismatch) || errors.Is(err, transport.ErrRejected):
			t.Fatalf("send %d failed with a protocol error %v; want nil or a network error", i, err)
		}
	}
	if ok < transport.MaxConns {
		t.Fatalf("only %d of %d sends succeeded, want at least MaxConns = %d", ok, n, transport.MaxConns)
	}
	seen := r.payloads()
	if len(seen) < ok {
		t.Fatalf("handler saw %d payloads but %d sends returned nil", len(seen), ok)
	}
	for _, p := range seen {
		known := false
		for _, s := range sent {
			if bytes.Equal(p, s) {
				known = true
				break
			}
		}
		if !known {
			t.Fatalf("handler saw unknown payload %q: frames were corrupted or mixed", p)
		}
	}
}

func TestSendToDeadAddressFailsWithinDialTimeout(t *testing.T) {
	start := time.Now()
	err := transport.Send(context.Background(), "127.0.0.1:1", []byte("x"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Send to a dead address returned nil")
	}
	if elapsed > transport.DialTimeout+time.Second {
		t.Fatalf("Send took %v, want at most DialTimeout + 1s = %v", elapsed, transport.DialTimeout+time.Second)
	}
}

func TestServeReturnsNilAfterContextCancel(t *testing.T) {
	s, err := transport.Listen("127.0.0.1:0", func(netip.Addr, []byte) error { return nil })
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of cancel")
	}
}

func TestHelloReturnsIdentityWithoutCallingHandler(t *testing.T) {
	var r recorder
	s, err := transport.Listen("127.0.0.1:0", r.handler(nil))
	if err != nil {
		t.Fatal(err)
	}
	s.SetIdentity(transport.Identity{ID: "abc123", Name: "MacBook", Version: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx)

	id, err := transport.Hello(context.Background(), s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != "abc123" || id.Name != "MacBook" || id.Version != 1 {
		t.Fatalf("identity = %+v", id)
	}
	if len(r.payloads()) != 0 {
		t.Fatal("hello reached the message handler")
	}
}

func TestHelloWithoutIdentityFails(t *testing.T) {
	var r recorder
	addr := startServer(t, r.handler(nil)) // no SetIdentity
	if _, err := transport.Hello(context.Background(), addr); err == nil {
		t.Fatal("hello succeeded against a server with no identity")
	}
}

func TestHelloToNonKopiFails(t *testing.T) {
	// A plain TCP server that answers garbage, like some other service on that port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			c.Close()
		}
	}()
	if _, err := transport.Hello(context.Background(), ln.Addr().String()); err == nil {
		t.Fatal("hello accepted a non-Kopi answer")
	}
	if _, err := transport.Hello(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("hello to a closed port succeeded")
	}
}

func TestHubBroadcastTracksOKAndMismatch(t *testing.T) {
	const good, bad = "127.0.0.1:47801", "127.0.0.1:47802"
	var (
		mu     sync.Mutex
		dialed []string
		sealed []byte
	)
	h := transport.NewHub()
	h.Send = func(_ context.Context, addr string, msg []byte) error {
		mu.Lock()
		dialed = append(dialed, addr)
		sealed = msg
		mu.Unlock()
		if addr != good {
			return transport.ErrPeerMismatch
		}
		return nil
	}
	h.Upsert(transport.Peer{ID: "a", Name: "Alpha", Addrs: []netip.AddrPort{netip.MustParseAddrPort(good)}})
	h.Upsert(transport.Peer{ID: "b", Name: "Beta", Addrs: []netip.AddrPort{netip.MustParseAddrPort(bad)}})
	h.Broadcast([]byte("x"))
	h.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 2 {
		t.Fatalf("fake Send dialed %d addresses (%v), want 2", len(dialed), dialed)
	}
	if !bytes.Equal(sealed, []byte("x")) {
		t.Fatalf("fake Send got payload %q, want %q", sealed, "x")
	}
	states := map[string]transport.State{}
	for _, p := range h.Peers() {
		states[p.ID] = p.State
	}
	if states["a"] != transport.StateOK {
		t.Fatalf("peer a state = %v, want StateOK", states["a"])
	}
	if states["b"] != transport.StateMismatch {
		t.Fatalf("peer b state = %v, want StateMismatch", states["b"])
	}
}

func TestHubUpsertMergesAddrsIPv4First(t *testing.T) {
	v6 := netip.MustParseAddrPort("[fe80::1]:47800")
	v4 := netip.MustParseAddrPort("127.0.0.1:47800")
	h := transport.NewHub()
	h.Upsert(transport.Peer{ID: "x", Name: "X", Addrs: []netip.AddrPort{v6}})
	h.Upsert(transport.Peer{ID: "x", Name: "X", Addrs: []netip.AddrPort{v4}})
	h.Upsert(transport.Peer{ID: "x", Name: "X", Addrs: []netip.AddrPort{v4}}) // duplicate must be ignored

	peers := h.Peers()
	if len(peers) != 1 {
		t.Fatalf("Peers() has %d entries, want 1", len(peers))
	}
	got := peers[0].Addrs
	if len(got) != 2 {
		t.Fatalf("peer has %d addrs (%v), want 2 merged without duplicates", len(got), got)
	}
	if got[0] != v4 || got[1] != v6 {
		t.Fatalf("addrs = %v, want IPv4 %v before IPv6 %v", got, v4, v6)
	}
}

func TestHubMarkMismatchFromMarksOnlyMatchingPeer(t *testing.T) {
	h := transport.NewHub()
	h.Upsert(transport.Peer{ID: "p1", Name: "One", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:47800")}})
	h.Upsert(transport.Peer{ID: "p2", Name: "Two", Addrs: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.2:47800")}})
	h.MarkMismatchFrom(netip.MustParseAddr("10.0.0.1"))

	states := map[string]transport.State{}
	for _, p := range h.Peers() {
		states[p.ID] = p.State
	}
	if states["p1"] != transport.StateMismatch {
		t.Fatalf("peer p1 state = %v, want StateMismatch", states["p1"])
	}
	if states["p2"] != transport.StateUnknown {
		t.Fatalf("peer p2 state = %v, want StateUnknown (wrong peer marked)", states["p2"])
	}
}
