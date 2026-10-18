//go:build darwin

package clip

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests use the real macOS pasteboard. They are opt-in because they replace
// what you have copied (it is restored afterwards). Run with:
//
//	KOPI_CLIPBOARD_TEST=1 go test ./internal/clip/ -v
func realClipboard(t *testing.T) Darwin {
	t.Helper()
	if os.Getenv("KOPI_CLIPBOARD_TEST") != "1" {
		t.Skip("set KOPI_CLIPBOARD_TEST=1 to test the real clipboard")
	}
	saved, err := read()
	t.Cleanup(func() {
		if err == nil {
			Darwin{}.Write(saved.Text)
		}
	})
	return Darwin{}
}

func TestDarwinWriteThenRead(t *testing.T) {
	c := realClipboard(t)
	for _, s := range []string{"hello", "", "héllo 🌍 你好", "line1\nline2", strings.Repeat("x", 1<<20)} {
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
			t.Fatal("plain text reported as sensitive")
		}
	}
}

func TestDarwinWatchSeesChangesIncludingOwnWrites(t *testing.T) {
	c := realClipboard(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := c.Watch(ctx)
	c.Write("watch-me-1")
	expect(t, ch, "watch-me-1")
	c.Write("watch-me-2")
	expect(t, ch, "watch-me-2")
}

func TestSENS1_DarwinDetectsConcealedAndTransient(t *testing.T) {
	realClipboard(t)
	for _, typ := range []string{"org.nspasteboard.ConcealedType", "org.nspasteboard.TransientType"} {
		if err := write("hunter2", typ); err != nil {
			t.Fatal(err)
		}
		it, err := read()
		if err != nil {
			t.Fatal(err)
		}
		if it.Text != "hunter2" || !it.Sensitive {
			t.Fatalf("%s: got %+v, want hunter2 marked sensitive", typ, it)
		}
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
