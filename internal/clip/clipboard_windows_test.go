//go:build windows

package clip

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These tests use the real Windows clipboard. They are opt-in because they replace
// what you have copied (it is restored afterwards). Run with:
//
//	KOPI_CLIPBOARD_TEST=1 go test ./internal/clip/ -v
func realClipboard(t *testing.T) Windows {
	t.Helper()
	if os.Getenv("KOPI_CLIPBOARD_TEST") != "1" {
		t.Skip("set KOPI_CLIPBOARD_TEST=1 to test the real clipboard")
	}
	saved, err := read()
	t.Cleanup(func() {
		if err == nil {
			Windows{}.Write(saved.Text)
		}
	})
	return Windows{}
}

func TestWindowsWriteThenRead(t *testing.T) {
	c := realClipboard(t)
	for _, s := range []string{"hello", "héllo 🌍 你好", "line1\r\nline2", strings.Repeat("x", 1<<20)} {
		if err := c.Write(s); err != nil {
			t.Fatalf("Write(%.20q): %v", s, err)
		}
		it, err := read()
		if err != nil {
			t.Fatalf("read after Write(%.20q): %v", s, err)
		}
		if it.Text != s {
			t.Fatalf("read %.40q, want %.40q", it.Text, s)
		}
		if it.Sensitive {
			t.Fatalf("plain text reported as sensitive")
		}
	}
}

func TestWindowsWatchSeesChangesIncludingOwnWrites(t *testing.T) {
	c := realClipboard(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := c.Watch(ctx)
	c.Write("watch-me-1")
	expect(t, ch, "watch-me-1")
	c.Write("watch-me-2")
	expect(t, ch, "watch-me-2")
}

func TestSENS1_WindowsDetectsExcludeFromMonitorFormat(t *testing.T) {
	realClipboard(t)
	writeWithMarker(t, "hunter2", fmtExcludeFromMonitor, nil)
	it, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if it.Text != "hunter2" || !it.Sensitive {
		t.Fatalf("got %+v, want text hunter2 marked sensitive", it)
	}
}

func TestSENS1_WindowsDetectsHistoryOptOut(t *testing.T) {
	realClipboard(t)
	writeWithMarker(t, "hunter3", fmtCanIncludeHistory, []byte{0, 0, 0, 0})
	it, _ := read()
	if !it.Sensitive {
		t.Fatal("CanIncludeInClipboardHistory=0 not treated as sensitive")
	}
	writeWithMarker(t, "fine", fmtCanIncludeHistory, []byte{1, 0, 0, 0})
	it, _ = read()
	if it.Sensitive {
		t.Fatal("CanIncludeInClipboardHistory=1 wrongly treated as sensitive")
	}
}

func expect(t *testing.T, ch <-chan Item, want string) {
	t.Helper()
	select {
	case it := <-ch:
		if it.Text != want {
			t.Fatalf("watch got %q, want %q", it.Text, want)
		}
	case <-time.After(3 * PollInterval):
		t.Fatalf("watch did not report %q", want)
	}
}

// writeWithMarker puts text on the clipboard together with an extra private format,
// the way a password manager does.
func writeWithMarker(t *testing.T, text string, format uintptr, data []byte) {
	t.Helper()
	unlock, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	procEmptyClipboard.Call()
	u16, _ := windows.UTF16FromString(text)
	setGlobal(t, cfUnicodeText, unsafe.Slice((*byte)(unsafe.Pointer(&u16[0])), len(u16)*2))
	if data == nil {
		data = []byte{0}
	}
	setGlobal(t, format, data)
}

func setGlobal(t *testing.T, format uintptr, data []byte) {
	t.Helper()
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		t.Fatal(err)
	}
	p, _, _ := procGlobalLock.Call(h)
	procMoveMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	procGlobalUnlock.Call(h)
	if r, _, err := procSetClipboardData.Call(format, h); r == 0 {
		t.Fatalf("SetClipboardData(%d): %v", format, err)
	}
}
