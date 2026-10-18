// Command mdnsprobe announces a fake Kopi device and prints every peer it discovers.
// It is a debugging aid for discovery problems on real networks:
//
//	go run ./tools/mdnsprobe -id probe1 -for 20s
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	dnssdlog "github.com/brutella/dnssd/log"

	"github.com/umidscodes/kopi/internal/discovery"
	"github.com/umidscodes/kopi/internal/transport"
)

func main() {
	host, _ := os.Hostname()
	id := flag.String("id", "probe", "device ID to announce")
	name := flag.String("name", host, "device name to announce")
	port := flag.Int("port", 47999, "port to announce (nothing listens on it)")
	dur := flag.Duration("for", 20*time.Second, "how long to run")
	debug := flag.Bool("debug", false, "log every mDNS packet sent and received")
	flag.Parse()
	if *debug {
		dnssdlog.Debug.Enable()
	}

	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	d := discovery.MDNS{Self: transport.Peer{ID: *id, Name: *name}, Port: *port}
	log.Printf("announcing %s (%s) on port %d for %v", *id, *name, *port, *dur)
	err := d.Run(ctx, func(ev discovery.Event) {
		if ev.Up {
			fmt.Printf("%s UP   %s %q %v\n", time.Now().Format("15:04:05.000"), ev.Peer.ID, ev.Peer.Name, ev.Peer.Addrs)
		} else {
			fmt.Printf("%s DOWN %s\n", time.Now().Format("15:04:05.000"), ev.Peer.ID)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
}
