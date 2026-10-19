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
	// The agent opens Kopi by bundle ID, so it is valid for any installed copy,
	// including one macOS runs from App Translocation.
	translocated := "/private/var/folders/nh/x/T/AppTranslocation/FC6C/d/Kopi.app/Contents/MacOS/kopi"
	if on, _ := Enabled(translocated); !on {
		t.Fatal("an agent written by the installed app must count for a translocated run too")
	}
	b, _ := os.ReadFile(agentPath())
	if strings.Contains(string(b), "AppTranslocation") || strings.Contains(string(b), "/Users/u/Applications") {
		t.Fatalf("plist must not contain an install path:\n%s", b)
	}
	for _, want := range []string{"<string>/usr/bin/open</string>", "<string>-b</string>", "<string>" + Label + "</string>", "<key>RunAtLoad</key><true/>"} {
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
