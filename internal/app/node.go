// Package app wires the clipboard, sync engine, transport and discovery into one
// running Kopi node. cmd/kopi adds the tray and config on top.
package app

import (
	"context"
	"errors"
	"net/netip"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/proto"
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
	server *transport.Server
	done   chan struct{}
}

// Start brings a node up. It runs until ctx is cancelled; Wait blocks until then.
func Start(ctx context.Context, o Options) (*Node, error) {
	hub := transport.NewHub()
	hub.OnChange = o.OnPeersChanged
	eng := ksync.New(ksync.Options{
		DeviceID:         o.DeviceID,
		Clipboard:        o.Clipboard,
		Network:          hub,
		Key:              o.Key,
		IncludeSensitive: o.IncludeSensitive,
		LineEnding:       o.LineEnding,
		OnEvent:          o.OnEvent,
	})
	srv, err := transport.Listen(o.ListenAddr, func(remote netip.Addr, sealed []byte) error {
		err := eng.Receive(sealed)
		switch {
		case errors.Is(err, proto.ErrDecrypt):
			hub.MarkMismatchFrom(remote)
		case errors.Is(err, proto.ErrBadVersion):
			hub.MarkNeedsUpdateFrom(remote) // RECENT-20: not applied
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	srv.SetIdentity(transport.Identity{ID: o.DeviceID, Name: o.DeviceName, Version: 1})
	disc := o.Discovery
	if disc == nil {
		self := transport.Peer{ID: o.DeviceID, Name: o.DeviceName}
		disc = discovery.Multi{
			discovery.MDNS{Self: self, Port: srv.Port()},
			discovery.Tailscale{Self: self, Port: srv.Port()},
		}
	}

	n := &Node{Engine: eng, Hub: hub, server: srv, done: make(chan struct{})}
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
