//go:build darwin

package clip

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

// Types apps add to mark pasteboard content as private (nspasteboard.org convention,
// used by 1Password, Bitwarden, KeePassXC and others).
static NSString *const kopiConcealed = @"org.nspasteboard.ConcealedType";
static NSString *const kopiTransient = @"org.nspasteboard.TransientType";

static long kopi_change_count(void) {
	@autoreleasepool {
		return (long)[[NSPasteboard generalPasteboard] changeCount];
	}
}

// kopi_read returns the pasteboard text as malloc'd UTF-8 (caller frees), or NULL
// when there is no text. *len gets the byte length; *sensitive is set to 1 when the
// content is marked private.
static char *kopi_read(size_t *len, int *sensitive) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSArray<NSPasteboardType> *types = [pb types];
		*sensitive = ([types containsObject:kopiConcealed] || [types containsObject:kopiTransient]) ? 1 : 0;
		NSString *s = [pb stringForType:NSPasteboardTypeString];
		if (s == nil) {
			return NULL;
		}
		NSData *d = [s dataUsingEncoding:NSUTF8StringEncoding];
		char *buf = malloc(d.length + 1);
		if (buf == NULL) {
			return NULL;
		}
		memcpy(buf, d.bytes, d.length);
		buf[d.length] = 0;
		*len = d.length;
		return buf;
	}
}

// kopi_write replaces the pasteboard with plain text. extraType, if not NULL, is
// declared as well (tests use it to imitate a password manager).
static int kopi_write(const char *bytes, size_t len, const char *extraType) {
	@autoreleasepool {
		NSString *s = [[NSString alloc] initWithBytes:bytes length:len encoding:NSUTF8StringEncoding];
		if (s == nil) {
			return 0;
		}
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		if (![pb setString:s forType:NSPasteboardTypeString]) {
			return 0;
		}
		if (extraType != NULL) {
			NSString *t = [NSString stringWithUTF8String:extraType];
			[pb setData:[NSData data] forType:t];
		}
		return 1;
	}
}
*/
import "C"

import (
	"context"
	"errors"
	"time"
	"unsafe"
)

const (
	// LineEnding is the convention for text written to this OS's clipboard.
	LineEnding = "\n"
	// PollInterval is how often the pasteboard change count is checked. macOS has
	// no change notification for the general pasteboard.
	PollInterval = 300 * time.Millisecond
)

// Darwin is the system clipboard (general pasteboard) on macOS.
type Darwin struct{}

var _ Clipboard = Darwin{}

// New returns the system clipboard.
func New() (Clipboard, error) { return Darwin{}, nil }

func (Darwin) Watch(ctx context.Context) (<-chan Item, error) {
	ch := make(chan Item, 8)
	last := C.kopi_change_count()
	go func() {
		defer close(ch)
		t := time.NewTicker(PollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			n := C.kopi_change_count()
			if n == last {
				continue
			}
			last = n
			it, err := read()
			if err != nil {
				continue // not text; nothing to sync
			}
			select {
			case ch <- it:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (Darwin) Write(text string) error { return write(text, "") }

func write(text, extraType string) error {
	b := C.CBytes([]byte(text + "\x00")) // never zero-length, so never NULL
	defer C.free(b)
	var extra *C.char
	if extraType != "" {
		extra = C.CString(extraType)
		defer C.free(unsafe.Pointer(extra))
	}
	if C.kopi_write((*C.char)(b), C.size_t(len(text)), extra) == 0 {
		return errors.New("kopi: could not write to the pasteboard")
	}
	return nil
}

func read() (Item, error) {
	var n C.size_t
	var sensitive C.int
	p := C.kopi_read(&n, &sensitive)
	if p == nil {
		return Item{}, errNoText
	}
	defer C.free(unsafe.Pointer(p))
	return Item{Text: C.GoStringN(p, C.int(n)), Sensitive: sensitive != 0}, nil
}
