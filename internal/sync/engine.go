// Package sync is Kopi's sync engine: it decides which local clipboard changes are
// sent to peers and which received clips are applied locally. It knows nothing about
// sockets or OS clipboards; those come in through interfaces.
package sync

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	gosync "sync"
	"time"

	"github.com/umidscodes/kopi/internal/clip"
	"github.com/umidscodes/kopi/internal/proto"
)

// ErrNoPassphrase is returned by Receive when no passphrase is configured (SEC-4).
var ErrNoPassphrase = errors.New("kopi: no passphrase configured")

// Broadcaster delivers a sealed message to every known peer.
type Broadcaster interface {
	Broadcast(sealed []byte)
}

// EventKind describes something the UI may want to show.
type EventKind int

const (
	EventSent EventKind = iota
	EventApplied
	EventSkippedSensitive
	EventTooLarge
	EventNoPassphrase
)

// Event is emitted for UI feedback. It never contains clip text.
type Event struct {
	Kind EventKind
	Size int
}

// Recorder is notified of every clip this device actually synced, for the
// "Recent clips" list. text is normalized ("\n" line endings), from is the device ID
// of the sender, at is when this device sent or applied the clip (RECENT-10).
// Sensitive clips are never passed to Record (RECENT-18, RECENT-19). Like OnEvent,
// Record must not block and must not call back into the Engine.
type Recorder interface {
	Record(text, from string, at time.Time)
}

// Options configures an Engine.
type Options struct {
	DeviceID  string
	Clipboard clip.Clipboard
	Network   Broadcaster
	// Key is nil when no passphrase is set; the engine then neither sends nor accepts.
	Key              *proto.Key
	IncludeSensitive bool
	// LineEnding is used when writing received clips: "\r\n" on Windows, "\n" elsewhere.
	LineEnding string
	// OnEvent, if set, is called for UI-relevant events. Must not block.
	OnEvent func(Event)
	// Recent, if set, records clips this device actually synced (nil = no list).
	Recent Recorder
	// Now and ReplayWindow are overridable for tests.
	Now          func() time.Time
	ReplayWindow time.Duration
}

// Engine syncs one device's clipboard with its peers.
type Engine struct {
	opts  Options
	guard *proto.ReplayGuard

	mu               gosync.Mutex
	key              *proto.Key // nil: no passphrase (SEC-4); changeable via SetKey
	paused           bool
	includeSensitive bool
	// Hash of the last clip applied from a peer. Local changes with this hash are the
	// echo of our own Write and must not be re-sent (SYNC-4). Cleared by any other
	// local change, so copying that same text again later still syncs (SYNC-5).
	appliedHash [32]byte
	hasApplied  bool
	lastAt      time.Time // timestamp of the newest clip seen or sent (SYNC-6)
	lastFrom    string
	hasLast     bool
}

// New creates an Engine. Call Run to start watching the clipboard.
func New(o Options) *Engine {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ReplayWindow == 0 {
		o.ReplayWindow = 2 * time.Minute
	}
	if o.LineEnding == "" {
		o.LineEnding = "\n"
	}
	g := proto.NewReplayGuard(o.ReplayWindow)
	g.Now = o.Now
	return &Engine{opts: o, guard: g, key: o.Key, includeSensitive: o.IncludeSensitive}
}

// SetKey switches to a new passphrase key (nil disables syncing). Takes effect for the
// next clip in either direction.
func (e *Engine) SetKey(k *proto.Key) {
	e.mu.Lock()
	e.key = k
	e.mu.Unlock()
}

// HasKey reports whether a passphrase is set.
func (e *Engine) HasKey() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.key != nil
}

// Paused reports whether syncing is paused.
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// Run handles local clipboard changes until ctx is done.
func (e *Engine) Run(ctx context.Context) error {
	ch, err := e.opts.Clipboard.Watch(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case it := <-ch:
			e.HandleLocal(it)
		}
	}
}

// SetPaused stops (or resumes) syncing in both directions (TRAY-2).
func (e *Engine) SetPaused(p bool) {
	e.mu.Lock()
	e.paused = p
	e.mu.Unlock()
}

// SetIncludeSensitive toggles syncing of sensitive clips (SENS-2).
func (e *Engine) SetIncludeSensitive(v bool) {
	e.mu.Lock()
	e.includeSensitive = v
	e.mu.Unlock()
}

// HandleLocal processes one local clipboard change.
func (e *Engine) HandleLocal(it clip.Item) {
	text := normalize(it.Text)
	h := sha256.Sum256([]byte(text))

	e.mu.Lock()
	if e.hasApplied && h == e.appliedHash {
		// Echo of a clip we just applied from a peer.
		e.mu.Unlock()
		return
	}
	e.hasApplied = false
	switch {
	case e.paused:
		e.mu.Unlock()
		return
	case e.key == nil:
		e.mu.Unlock()
		e.emit(Event{Kind: EventNoPassphrase})
		return
	case it.Sensitive && !e.includeSensitive:
		e.mu.Unlock()
		e.emit(Event{Kind: EventSkippedSensitive})
		return
	case len(text) > proto.MaxTextBytes:
		e.mu.Unlock()
		e.emit(Event{Kind: EventTooLarge, Size: len(text)})
		return
	}
	// Timestamps are millisecond precision on the wire. Keep them strictly increasing so
	// two quick copies (or a copy right after receiving a clip) never tie or go backwards.
	at := e.opts.Now().Truncate(time.Millisecond)
	if e.hasLast && !at.After(e.lastAt) {
		at = e.lastAt.Add(time.Millisecond)
	}
	m := proto.Message{ID: proto.NewID(), From: e.opts.DeviceID, SentAt: at, Text: text, Sensitive: it.Sensitive}
	e.lastAt, e.lastFrom, e.hasLast = m.SentAt, m.From, true
	key := *e.key
	e.mu.Unlock()

	sealed, err := proto.Seal(key, m)
	if err != nil {
		return
	}
	e.opts.Network.Broadcast(sealed)
	if !it.Sensitive {
		// Record the moment this device sent, not the wire timestamp: `at` can be
		// bumped past our own clock by the SYNC-6 rule (RECENT-10).
		e.record(text, e.opts.DeviceID, e.opts.Now())
	}
	e.emit(Event{Kind: EventSent, Size: len(text)})
}

// Receive processes a sealed message from a peer. It returns proto.ErrDecrypt when
// the peer uses a different passphrase, so the caller can mark that peer (SEC-1).
func (e *Engine) Receive(sealed []byte) error {
	e.mu.Lock()
	kp := e.key
	e.mu.Unlock()
	if kp == nil {
		return ErrNoPassphrase
	}
	m, err := proto.Open(*kp, sealed)
	if err != nil {
		return err
	}
	if m.From == e.opts.DeviceID {
		return nil
	}
	if err := e.guard.Check(m); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return nil
	}
	if e.hasLast && !newer(m.SentAt, m.From, e.lastAt, e.lastFrom) {
		return nil // an older clip lost to a newer one (SYNC-6)
	}
	text := normalize(m.Text)
	e.appliedHash = sha256.Sum256([]byte(text))
	e.hasApplied = true
	e.lastAt, e.lastFrom, e.hasLast = m.SentAt, m.From, true
	write := text
	if e.opts.LineEnding != "\n" {
		write = strings.ReplaceAll(text, "\n", e.opts.LineEnding)
	}
	if err := e.opts.Clipboard.Write(write); err != nil {
		return err
	}
	if !m.Sensitive {
		e.record(text, m.From, e.opts.Now())
	}
	e.emit(Event{Kind: EventApplied, Size: len(text)})
	return nil
}

func (e *Engine) emit(ev Event) {
	if e.opts.OnEvent != nil {
		e.opts.OnEvent(ev)
	}
}

func (e *Engine) record(text, from string, at time.Time) {
	if e.opts.Recent != nil {
		e.opts.Recent.Record(text, from, at)
	}
}

// newer reports whether (t, from) is after (lastT, lastFrom). Equal timestamps are
// broken by device ID so every device picks the same winner.
func newer(t time.Time, from string, lastT time.Time, lastFrom string) bool {
	if !t.Equal(lastT) {
		return t.After(lastT)
	}
	return from > lastFrom
}

// normalize converts line endings to "\n" so the same text hashes the same on every OS.
func normalize(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
