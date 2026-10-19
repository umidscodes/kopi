//go:build !darwin

package discovery

import (
	"net"
	"testing"

	"github.com/brutella/dnssd"

	"github.com/umidscodes/kopi/internal/proto"
)

func TestRECENT21_PeerFromReadsVersion(t *testing.T) {
	entry := func(v string, has bool) dnssd.BrowseEntry {
		txt := map[string]string{"id": "abc", "name": "mac"}
		if has {
			txt["v"] = v
		}
		return dnssd.BrowseEntry{IPs: []net.IP{net.ParseIP("10.0.0.5")}, Port: 47800, Text: txt}
	}
	for _, c := range []struct {
		v    string
		has  bool
		want int
	}{{"1", true, 1}, {"2", true, proto.Version}, {"", true, 0}, {"abc", true, 0}, {"", false, 0}} {
		p, ok := peerFrom(entry(c.v, c.has))
		if !ok {
			t.Fatalf("v=%q: peerFrom rejected a valid entry", c.v)
		}
		if p.Version != c.want {
			t.Errorf("v=%q present=%v: Version = %d, want %d", c.v, c.has, p.Version, c.want)
		}
	}
}
