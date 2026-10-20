package main

import (
	"context"
	"testing"

	"github.com/umidscodes/kopi/internal/app"
	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/transport"
)

// RECENT-13: both the tray dialog and a kopi.yml edit go through applyPassphrase,
// which must empty the Recent clips list.
func TestRECENT13_ApplyPassphraseEmptiesList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n, err := app.Start(ctx, app.Options{
		DeviceID: "a", DeviceName: "a", Key: keyFor("old"), Clipboard: clip.NewFake(),
		ListenAddr: "127.0.0.1:0", Discovery: discovery.NewFakeLAN().Join(transport.Peer{ID: "a"}),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); n.Wait() })

	n.Engine.HandleLocal(clip.Item{Text: "x"})
	if es := n.Recent.Entries(); len(es) != 1 {
		t.Fatalf("list before the change = %+v, want [x]", es)
	}

	a := &kopiApp{node: n} // markDirty never blocks, even without a refresher
	a.applyPassphrase("new")
	if es := n.Recent.Entries(); len(es) != 0 {
		t.Fatalf("list after applyPassphrase = %+v, want empty", es)
	}
	if !n.Engine.HasKey() {
		t.Fatal("no key after applyPassphrase")
	}
}
