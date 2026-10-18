// Command kopiping asks a Kopi node "who are you?" — a quick check that it is running
// and reachable through firewalls.
//
//	go run ./tools/kopiping 100.125.24.127:47800
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/umidscodes/kopi/internal/transport"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: kopiping host:port ...")
		os.Exit(2)
	}
	failed := false
	for _, addr := range os.Args[1:] {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		id, err := transport.Hello(ctx, addr)
		cancel()
		if err != nil {
			fmt.Printf("%-24s FAIL %v\n", addr, err)
			failed = true
			continue
		}
		fmt.Printf("%-24s OK   %q (%.6s) in %v\n", addr, id.Name, id.ID, time.Since(start).Round(time.Millisecond))
	}
	if failed {
		os.Exit(1)
	}
}
