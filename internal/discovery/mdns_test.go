package discovery

import (
	"context"
	"os"
	gosync "sync"
	"testing"
	"time"

	"github.com/umidscodes/kopi/internal/transport"
)

// Real multicast DNS on the machine's network. Opt-in: on Windows the first run of
// each new test binary triggers a firewall prompt, and CI runners often block
// multicast. Run manually with:
//
//	KOPI_MDNS_TEST=1 go test ./internal/discovery/ -run MDNS -v
func TestDISC_MDNS_TwoDevicesFindAndLoseEachOther(t *testing.T) {
	if os.Getenv("KOPI_MDNS_TEST") != "1" {
		t.Skip("set KOPI_MDNS_TEST=1 to run the real mDNS test")
	}
	type seen struct {
		mu   gosync.Mutex
		up   map[string]transport.Peer
		down map[string]bool
	}
	run := func(ctx context.Context, id string, port int, s *seen) {
		d := MDNS{Self: transport.Peer{ID: id, Name: "kopi-test-" + id}, Port: port}
		go d.Run(ctx, func(ev Event) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if ev.Up {
				s.up[ev.Peer.ID] = ev.Peer
				delete(s.down, ev.Peer.ID)
			} else {
				s.down[ev.Peer.ID] = true
			}
		})
	}
	newSeen := func() *seen { return &seen{up: map[string]transport.Peer{}, down: map[string]bool{}} }

	idA, idB := "testA"+time.Now().Format("150405"), "testB"+time.Now().Format("150405")
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	sa, sb := newSeen(), newSeen()
	run(ctxA, idA, 47901, sa)
	run(ctxB, idB, 47902, sb)

	wait := func(what string, timeout time.Duration, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out after %v: %s", timeout, what)
	}
	// DISC-1: within 10 seconds.
	wait("A finds B", 10*time.Second, func() bool {
		sa.mu.Lock()
		defer sa.mu.Unlock()
		p, ok := sa.up[idB]
		return ok && len(p.Addrs) > 0 && p.Addrs[0].Port() == 47902
	})
	wait("B finds A", 10*time.Second, func() bool {
		sb.mu.Lock()
		defer sb.mu.Unlock()
		_, ok := sb.up[idA]
		return ok
	})
	sa.mu.Lock()
	t.Logf("A sees B at %v", sa.up[idB].Addrs)
	sa.mu.Unlock()

	// DISC-2: B quits cleanly; A must notice within 30 seconds.
	cancelB()
	wait("A notices B left", 30*time.Second, func() bool {
		sa.mu.Lock()
		defer sa.mu.Unlock()
		return sa.down[idB]
	})
}
