//go:build windows

package clip

import (
	"context"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procRegisterClipboardFormatW   = user32.NewProc("RegisterClipboardFormatW")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
	// RtlMoveMemory copies between OS-owned and Go memory without converting a
	// uintptr back into a Go pointer (which go vet rightly flags as unsafe).
	procMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	// PollInterval is how often the clipboard sequence number is checked.
	PollInterval = 250 * time.Millisecond
)

// Formats apps set to mark clipboard content as private. Password managers use them.
var (
	fmtExcludeFromMonitor = registerFormat("ExcludeClipboardContentFromMonitorProcessing")
	fmtCanIncludeHistory  = registerFormat("CanIncludeInClipboardHistory")
	fmtViewerIgnore       = registerFormat("Clipboard Viewer Ignore") // older convention (e.g. KeePass)
)

// Windows is the system clipboard on Windows.
type Windows struct{}

var _ Clipboard = Windows{}

// New returns the system clipboard.
func New() (Clipboard, error) { return Windows{}, nil }

// LineEnding is the convention for text written to this OS's clipboard.
const LineEnding = "\r\n"

func (Windows) Watch(ctx context.Context) (<-chan Item, error) {
	ch := make(chan Item, 8)
	// Take the starting point now, not inside the goroutine, so a change made right
	// after Watch returns is never mistaken for the starting state.
	last := sequenceNumber()
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
			seq := sequenceNumber()
			if seq == last {
				continue
			}
			last = seq
			it, err := read()
			if err != nil {
				continue // not text (an image, files…) or clipboard busy; nothing to sync
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

func (Windows) Write(text string) error {
	u16, err := windows.UTF16FromString(text)
	if err != nil {
		return err // text contains NUL; not representable as CF_UNICODETEXT
	}
	size := uintptr(len(u16)) * 2
	h, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return err
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return err
	}
	procMoveMemory.Call(p, uintptr(unsafe.Pointer(&u16[0])), size)
	procGlobalUnlock.Call(h)

	unlock, err := open()
	if err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer unlock()
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		procGlobalFree.Call(h)
		return err
	}
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return err
	}
	// On success the system owns h; it must not be freed.
	return nil
}

// read returns the current clipboard text and whether it is marked private.
func read() (Item, error) {
	unlock, err := open()
	if err != nil {
		return Item{}, err
	}
	defer unlock()
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return Item{}, errNoText
	}
	h, _, err := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return Item{}, err
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		return Item{}, err
	}
	n, _, _ := procGlobalSize.Call(h)
	buf := make([]uint16, n/2)
	if len(buf) > 0 {
		procMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, uintptr(len(buf))*2)
	}
	procGlobalUnlock.Call(h)
	return Item{Text: windows.UTF16ToString(buf), Sensitive: sensitive()}, nil
}

// sensitive must be called with the clipboard open.
func sensitive() bool {
	if available(fmtExcludeFromMonitor) || available(fmtViewerIgnore) {
		return true
	}
	// CanIncludeInClipboardHistory = 0 means "don't keep this", as password managers set it.
	if available(fmtCanIncludeHistory) {
		if v, ok := readDWORD(fmtCanIncludeHistory); ok && v == 0 {
			return true
		}
	}
	return false
}

func available(format uintptr) bool {
	if format == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(format)
	return r != 0
}

func readDWORD(format uintptr) (uint32, bool) {
	h, _, _ := procGetClipboardData.Call(format)
	if h == 0 {
		return 0, false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return 0, false
	}
	defer procGlobalUnlock.Call(h)
	if n, _, _ := procGlobalSize.Call(h); n < 4 {
		return 0, false
	}
	var v uint32
	procMoveMemory.Call(uintptr(unsafe.Pointer(&v)), p, 4)
	return v, true
}

// open opens the clipboard, retrying briefly because other apps hold it for short
// moments. The OS thread stays locked until unlock: the clipboard is opened per thread.
func open() (unlock func(), err error) {
	runtime.LockOSThread()
	for i := 0; i < 20; i++ {
		var r uintptr
		r, _, err = procOpenClipboard.Call(0)
		if r != 0 {
			return func() {
				procCloseClipboard.Call()
				runtime.UnlockOSThread()
			}, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	runtime.UnlockOSThread()
	return nil, err
}

func sequenceNumber() uintptr {
	r, _, _ := procGetClipboardSequenceNumber.Call()
	return r
}

func registerFormat(name string) uintptr {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return r
}
