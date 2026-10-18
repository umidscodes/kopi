// Command kopi keeps plain-text clipboards in sync across your devices on the LAN.
//
//	kopi              run in the system tray (the normal way to start it)
//	kopi passphrase   set the shared passphrase
//	kopi version      print the version
package main

import (
	"fmt"
	"os"
	"runtime"
)

var version = "dev" // set with -ldflags "-X main.version=v0.1.0"

func init() {
	// The tray's event loop must run on the main OS thread (required on macOS).
	runtime.LockOSThread()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "passphrase":
			os.Exit(cmdPassphrase())
		case "version", "--version", "-v":
			fmt.Println("kopi", version)
			return
		case "help", "--help", "-h":
			usage()
			return
		default:
			fmt.Fprintf(os.Stderr, "kopi: unknown command %q\n\n", os.Args[1])
			usage()
			os.Exit(2)
		}
	}
	runApp()
}

func usage() {
	fmt.Println(`Usage:
  kopi              run in the system tray
  kopi passphrase   set the shared passphrase (same on every device)
  kopi version      print the version`)
}
