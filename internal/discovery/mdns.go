package discovery

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/brutella/dnssd"

	"github.com/umidscodes/kopi/internal/transport"
)

// ServiceType is Kopi's DNS-SD service type.
const ServiceType = "_kopi._tcp"

// MDNS announces this device with mDNS/DNS-SD and browses for others.
// Only device ID, name and protocol version are published; nothing secret.
type MDNS struct {
	Self transport.Peer // ID and Name; Addrs are ignored
	Port int
}

func (m MDNS) Run(ctx context.Context, emit func(Event)) error {
	cfg := dnssd.Config{
		// Instance names must be unique on the LAN; the ID suffix keeps two devices
		// with the same hostname apart.
		Name: fmt.Sprintf("%s-%.6s", m.Self.Name, m.Self.ID),
		Type: ServiceType,
		Port: m.Port,
		Text: map[string]string{"id": m.Self.ID, "name": m.Self.Name, "v": "1"},
	}
	svc, err := dnssd.NewService(cfg)
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

	errc := make(chan error, 2)
	// Respond sends goodbye packets when ctx is cancelled, so peers drop us at once (DISC-2).
	go func() { errc <- rp.Respond(ctx) }()
	go func() {
		errc <- dnssd.LookupType(ctx, ServiceType+".local.",
			func(e dnssd.BrowseEntry) {
				if p, ok := peerFrom(e); ok && p.ID != m.Self.ID {
					emit(Event{Up: true, Peer: p})
				}
			},
			func(e dnssd.BrowseEntry) {
				if id := e.Text["id"]; id != "" && id != m.Self.ID {
					emit(Event{Up: false, Peer: transport.Peer{ID: id}})
				}
			})
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func peerFrom(e dnssd.BrowseEntry) (transport.Peer, bool) {
	id := e.Text["id"]
	if id == "" || e.Port <= 0 || e.Port > 65535 {
		return transport.Peer{}, false
	}
	p := transport.Peer{ID: id, Name: e.Text["name"]}
	for _, ip := range e.IPs {
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		a = a.Unmap()
		if a.Is6() && a.IsLinkLocalUnicast() {
			// Needs an interface zone to dial; IPv4 or global IPv6 will do.
			continue
		}
		p.Addrs = append(p.Addrs, netip.AddrPortFrom(a, uint16(e.Port)))
	}
	if len(p.Addrs) == 0 {
		// Fall back to resolving the advertised host name.
		if ips, err := net.LookupIP(e.Host); err == nil {
			for _, ip := range ips {
				if a, ok := netip.AddrFromSlice(ip); ok && !a.Unmap().IsLinkLocalUnicast() {
					p.Addrs = append(p.Addrs, netip.AddrPortFrom(a.Unmap(), uint16(e.Port)))
				}
			}
		}
	}
	return p, len(p.Addrs) > 0
}
