package autostart

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// Uses a throwaway key under HKCU\Software\KopiTest, never the real Run key, and
// deletes it afterwards.
func TestWindowsEnableDisableRoundTrip(t *testing.T) {
	const testKey = `Software\KopiTest\Run`
	old := runKey
	runKey = testKey
	t.Cleanup(func() {
		runKey = old
		registry.DeleteKey(registry.CURRENT_USER, testKey)
		registry.DeleteKey(registry.CURRENT_USER, `Software\KopiTest`)
	})

	exe := `C:\Users\someone\AppData\Local\Programs\Kopi\kopi.exe`
	if on, err := Enabled(exe); err != nil || on {
		t.Fatalf("before Enable: on=%v err=%v", on, err)
	}
	if err := Enable(exe); err != nil {
		t.Fatal(err)
	}
	if on, err := Enabled(exe); err != nil || !on {
		t.Fatalf("after Enable: on=%v err=%v", on, err)
	}
	if on, _ := Enabled(`C:\elsewhere\kopi.exe`); on {
		t.Fatal("an entry for another copy of Kopi must not count as enabled")
	}
	k, _ := registry.OpenKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE)
	v, _, _ := k.GetStringValue("Kopi")
	k.Close()
	if v != `"`+exe+`"` {
		t.Fatalf("stored %q, want the quoted path", v)
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
