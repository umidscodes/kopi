//go:build !darwin

package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/brutella/dnssd"

	"github.com/umidscodes/kopi/internal/transport"
)

// MDNS announces this device with mDNS/DNS-SD and browses for others.
// Only device ID, name and protocol version are published; nothing secret.
//
// This implementation runs its own mDNS stack (brutella/dnssd). On macOS the system
// mDNSResponder owns port 5353 and our socket never sees other hosts' multicast, so
// macOS uses the native Bonjour API instead (mdns_darwin.go).
type MDNS struct {
	Self transport.Peer // ID and Name; Addrs are ignored
	Port int
}

func (m MDNS) Run(ctx context.Context, emit func(Event)) error {
	svc, err := dnssd.NewService(dnssd.Config{
		Name: instanceName(m.Self),
		Type: ServiceType,
		Port: m.Port,
		Text: txtRecord(m.Self),
	})
	if err != nil {
		return fmt.Errorf("mdns service: %w", err)
	}
	rp, err := dnssd.NewResponder()
	if err != nil {
		return fmt.Errorf("mdns responder: %w", err)
	}
	if _, err := rp.Add(svc); err != nil {
		return fmt.Errorf("mdns add: %w", err)
	}
	respondErr := make(chan error, 1)
	// Respond sends goodbye packets when ctx is cancelled, so peers drop us at once.
	go func() { respondErr <- rp.Respond(ctx) }()

	b := newBrowser(m.Self.ID, emit)
	t := time.NewTicker(BrowseInterval)
	defer t.Stop()
	for {
		round(ctx, b)
		select {
		case <-ctx.Done():
			return nil
		case err := <-respondErr:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case <-t.C:
		}
	}
}

// round runs one browse of slightly less than BrowseInterval, then expires peers
// that haven't answered for PeerTimeout.
func round(ctx context.Context, b *browser) {
	rctx, cancel := context.WithTimeout(ctx, BrowseInterval-500*time.Millisecond)
	defer cancel()
	dnssd.LookupType(rctx, ServiceType+".local.",
		func(e dnssd.BrowseEntry) {
			if p, ok := peerFrom(e); ok {
				b.seen(p)
			}
		},
		func(e dnssd.BrowseEntry) {
			if id := e.Text["id"]; id != "" {
				b.gone(id) // goodbye packet: drop at once
			}
		})
	b.expire()
}

func peerFrom(e dnssd.BrowseEntry) (transport.Peer, bool) {
	id := e.Text["id"]
	if id == "" || e.Port <= 0 || e.Port > 65535 {
		return transport.Peer{}, false
	}
	p := transport.Peer{ID: id, Name: e.Text["name"]}
	for _, ip := range e.IPs {
		if a, ok := usable(ip); ok {
			p.Addrs = append(p.Addrs, netip.AddrPortFrom(a, uint16(e.Port)))
		}
	}
	if len(p.Addrs) == 0 {
		// Fall back to resolving the advertised host name.
		if ips, err := net.LookupIP(e.Host); err == nil {
			for _, ip := range ips {
				if a, ok := usable(ip); ok {
					p.Addrs = append(p.Addrs, netip.AddrPortFrom(a, uint16(e.Port)))
				}
			}
		}
	}
	return p, len(p.Addrs) > 0
}
