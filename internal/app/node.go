// Package app wires the clipboard, sync engine, transport and discovery into one
// running Kopi node. cmd/kopi adds the tray and config on top.
package app

import (
	"context"
	"errors"
	"net/netip"
	gosync "sync"
	"sync/atomic"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/proto"
	"github.com/umidscodes/kopi/internal/recent"
	ksync "github.com/umidscodes/kopi/internal/sync"
	"github.com/umidscodes/kopi/internal/transport"
)

// Options configures a Node.
type Options struct {
	DeviceID   string
	DeviceName string
	Key        *proto.Key // nil: no passphrase set (SEC-4)
	Clipboard  clip.Clipboard
	// ListenAddr is where to accept peers, e.g. ":47800". Tests use "127.0.0.1:0".
	ListenAddr string
	// Discovery, if nil, defaults to mDNS on the listening port.
	Discovery        discovery.Discovery
	IncludeSensitive bool
	LineEnding       string
	OnEvent          func(ksync.Event)
	OnPeersChanged   func()
}

// Node is one running Kopi device.
type Node struct {
	Engine *ksync.Engine
	Hub    *transport.Hub
	// Recent is this device's "Recent clips" list, fed by Engine.
	Recent *recent.List
	server *transport.Server
	done   chan struct{}

	// names is a snapshot of the hub's peer names (ID -> name), read without a lock
	// by Recent.NameOf. The engine records clips while holding its own lock, so the
	// lookup must not wait on anything (reviewer note on PR #24). namesMu only orders
	// refreshes so an older snapshot never overwrites a newer one.
	names   atomic.Pointer[map[string]string]
	namesMu gosync.Mutex
}

// Start brings a node up. It runs until ctx is cancelled; Wait blocks until then.
func Start(ctx context.Context, o Options) (*Node, error) {
	n := &Node{Hub: transport.NewHub(), Recent: &recent.List{}, done: make(chan struct{})}
	hub := n.Hub
	// Decision 4: the sender's display name is stored when the clip is recorded.
	n.Recent.NameOf = n.nameOf
	hub.OnChange = func() {
		n.refreshNames()
		if o.OnPeersChanged != nil {
			o.OnPeersChanged()
		}
	}
	eng := ksync.New(ksync.Options{
		DeviceID:         o.DeviceID,
		Clipboard:        o.Clipboard,
		Network:          hub,
		Key:              o.Key,
		IncludeSensitive: o.IncludeSensitive,
		LineEnding:       o.LineEnding,
		OnEvent:          o.OnEvent,
		Recent:           n.Recent,
	})
	n.Engine = eng
	srv, err := transport.Listen(o.ListenAddr, func(remote netip.Addr, sealed []byte) error {
		err := eng.Receive(sealed)
		switch {
		case err == nil:
			hub.ClearNeedsUpdateFrom(remote) // it speaks our version now
		case errors.Is(err, proto.ErrDecrypt):
			hub.ClearNeedsUpdateFrom(remote) // our version, other passphrase
			hub.MarkMismatchFrom(remote)
		case errors.Is(err, proto.ErrBadVersion):
			hub.MarkNeedsUpdateFrom(remote) // RECENT-20: not applied
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	srv.SetIdentity(transport.Identity{ID: o.DeviceID, Name: o.DeviceName, Version: proto.Version})
	disc := o.Discovery
	if disc == nil {
		self := transport.Peer{ID: o.DeviceID, Name: o.DeviceName}
		disc = discovery.Multi{
			discovery.MDNS{Self: self, Port: srv.Port()},
			discovery.Tailscale{Self: self, Port: srv.Port()},
		}
	}

	n.server = srv
	errc := make(chan error, 3)
	go func() { errc <- srv.Serve(ctx) }()
	go func() { errc <- eng.Run(ctx) }()
	go func() {
		errc <- disc.Run(ctx, func(ev discovery.Event) {
			if ev.Up {
				hub.Upsert(ev.Peer)
			} else {
				hub.Remove(ev.Peer.ID)
			}
		})
	}()
	go func() {
		for i := 0; i < 3; i++ {
			<-errc
		}
		hub.Stop() // don't keep retrying sends after shutdown
		hub.Wait()
		close(n.done)
	}()
	return n, nil
}

// Port is the TCP port the node listens on.
func (n *Node) Port() int { return n.server.Port() }

// Wait blocks until the node has fully stopped.
func (n *Node) Wait() { <-n.done }

// SetKey switches to a new passphrase (nil: none). Old peer verdicts no longer hold,
// and the Recent clips list is emptied (RECENT-13). Pausing never touches the list
// (RECENT-15).
func (n *Node) SetKey(k *proto.Key) {
	n.Engine.SetKey(k)
	n.Hub.ResetStates()
	n.Recent.Clear()
}

// nameOf returns the display name of a peer as last seen by the hub, "" if unknown.
// Lock-free: it is called by the engine with the engine's lock held.
func (n *Node) nameOf(id string) string {
	if m := n.names.Load(); m != nil {
		return (*m)[id]
	}
	return ""
}

// refreshNames rebuilds the name snapshot from the hub. It runs from Hub.OnChange,
// which the hub calls after releasing its lock; it takes hub.mu (via Peers) but never
// the engine's lock, and the hub never calls into the engine.
func (n *Node) refreshNames() {
	n.namesMu.Lock()
	defer n.namesMu.Unlock()
	m := map[string]string{}
	for _, p := range n.Hub.Peers() {
		if p.Name != "" {
			m[p.ID] = p.Name
		}
	}
	n.names.Store(&m)
}
