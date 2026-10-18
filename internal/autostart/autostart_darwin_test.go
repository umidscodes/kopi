package autostart

import (
	"os"
	"strings"
	"testing"
)

func TestDarwinEnableDisableRoundTrip(t *testing.T) {
	old := agentDir
	agentDir = t.TempDir() // never touches the real ~/Library/LaunchAgents
	t.Cleanup(func() { agentDir = old })

	exe := "/Users/u/Applications/Kopi.app/Contents/MacOS/kopi"
	if on, err := Enabled(exe); err != nil || on {
		t.Fatalf("before Enable: on=%v err=%v", on, err)
	}
	if err := Enable(exe); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(exe); err != nil || !on {
		t.Fatalf("after Enable: on=%v err=%v", on, err)
	}
	if on, _ := Enabled("/somewhere/else/Kopi.app/Contents/MacOS/kopi"); on {
		t.Fatal("an agent for another copy must not count as enabled")
	}
	b, _ := os.ReadFile(agentPath())
	for _, want := range []string{"<string>/usr/bin/open</string>", "<string>/Users/u/Applications/Kopi.app</string>", "<key>RunAtLoad</key><true/>", Label} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist missing %q:\n%s", want, b)
		}
	}
	if err := Disable(); err != nil {
		t.Fatal(err)
	}
	if on, _ := Enabled(exe); on {
		t.Fatal("still enabled after Disable")
	}
	if err := Disable(); err != nil {
		t.Fatalf("second Disable should be a no-op, got %v", err)
	}
}

func TestDarwinPlistForBareBinaryRunsItDirectly(t *testing.T) {
	p := string(plist("/Users/u/kopi/bin/kopi"))
	if strings.Contains(p, "/usr/bin/open") || !strings.Contains(p, "<string>/Users/u/kopi/bin/kopi</string>") {
		t.Fatalf("unexpected plist for a bare binary:\n%s", p)
	}
}
