package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

var (
	keyA = DeriveKey("alpha")
	keyB = DeriveKey("bravo")
)

func msg(text string) Message {
	return Message{ID: NewID(), From: "device-a", SentAt: time.UnixMilli(1_790_000_000_000), Text: text}
}

func TestSealOpenRoundTrip(t *testing.T) {
	in := msg("héllo 🌍 你好\nline2")
	sealed, err := Seal(keyA, in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Open(keyA, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.From != in.From || !out.SentAt.Equal(in.SentAt) || out.Text != in.Text {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestDeriveKeyIsDeterministic(t *testing.T) {
	if DeriveKey("alpha") != keyA {
		t.Fatal("same passphrase must give the same key on every device")
	}
	if keyA == keyB {
		t.Fatal("different passphrases gave the same key")
	}
}

func TestSEC1_WrongKeyCannotOpen(t *testing.T) {
	sealed, _ := Seal(keyA, msg("secret"))
	if _, err := Open(keyB, sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v, want ErrDecrypt", err)
	}
}

func TestSEC2_CiphertextHidesText(t *testing.T) {
	const text = "my-bank-password-1234"
	sealed, _ := Seal(keyA, msg(text))
	if bytes.Contains(sealed, []byte(text)) {
		t.Fatal("clip text visible in sealed message")
	}
	if bytes.Contains(sealed, []byte("device-a")) {
		t.Fatal("sender id visible in sealed message")
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	m := msg("same")
	a, _ := Seal(keyA, m)
	b, _ := Seal(keyA, m)
	if bytes.Equal(a, b) {
		t.Fatal("sealing the same message twice produced identical bytes")
	}
}

func TestTamperedMessageRejected(t *testing.T) {
	sealed, _ := Seal(keyA, msg("hello"))
	for _, i := range []int{0, 5, len(sealed) - 1} {
		bad := append([]byte(nil), sealed...)
		bad[i] ^= 0x01
		if _, err := Open(keyA, bad); err == nil {
			t.Fatalf("flipping byte %d was not detected", i)
		}
	}
}

func TestOpenRejectsGarbage(t *testing.T) {
	for _, in := range [][]byte{nil, {1}, bytes.Repeat([]byte{1}, 40)} {
		if _, err := Open(keyA, in); err == nil {
			t.Fatalf("Open(%v) succeeded", in)
		}
	}
}

func TestSIZE1_SealRejectsOversizedText(t *testing.T) {
	if _, err := Seal(keyA, msg(strings.Repeat("x", MaxTextBytes+1))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestSIZE3_ExactlyAtLimitFitsInAFrame(t *testing.T) {
	m := msg(strings.Repeat("x", MaxTextBytes))
	m.From = strings.Repeat("f", MaxFromBytes)
	sealed, err := Seal(keyA, m)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, sealed); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Open(keyA, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Text) != MaxTextBytes {
		t.Fatalf("text length %d, want %d", len(out.Text), MaxTextBytes)
	}
}

// countingReader records how many bytes were read from it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestSIZE2_OversizedFrameRejectedBeforeReadingBody(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameBytes+1)
	body := bytes.Repeat([]byte{0}, 1024)
	cr := &countingReader{r: io.MultiReader(bytes.NewReader(hdr[:]), bytes.NewReader(body))}
	if _, err := ReadFrame(cr); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	if cr.n != 4 {
		t.Fatalf("read %d bytes, want only the 4-byte header", cr.n)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	for _, p := range [][]byte{[]byte("one"), {}, []byte("three")} {
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"one", "", "three"} {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestSEC3_ReplayGuard(t *testing.T) {
	now := time.UnixMilli(1_790_000_000_000)
	g := NewReplayGuard(2 * time.Minute)
	g.Now = func() time.Time { return now }

	m := msg("x")
	m.SentAt = now
	if err := g.Check(m); err != nil {
		t.Fatalf("first delivery rejected: %v", err)
	}
	if err := g.Check(m); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: got %v, want ErrReplay", err)
	}

	old := msg("old")
	old.SentAt = now.Add(-3 * time.Minute)
	if err := g.Check(old); !errors.Is(err, ErrStale) {
		t.Fatalf("old message: got %v, want ErrStale", err)
	}
	future := msg("future")
	future.SentAt = now.Add(3 * time.Minute)
	if err := g.Check(future); !errors.Is(err, ErrStale) {
		t.Fatalf("future message: got %v, want ErrStale", err)
	}
}

func TestSEC3_ReplayRejectedForWholeFreshnessWindow(t *testing.T) {
	// A message that arrives early (sender clock 2 min ahead) stays fresh for 4 minutes
	// of receiver time. A replay at any point in that span must still be caught.
	now := time.UnixMilli(1_790_000_000_000)
	g := NewReplayGuard(2 * time.Minute)
	g.Now = func() time.Time { return now }
	m := msg("x")
	m.SentAt = now.Add(2 * time.Minute)
	if err := g.Check(m); err != nil {
		t.Fatal(err)
	}
	for step := 0; step <= 8; step++ {
		now = now.Add(30 * time.Second)
		other := msg("other")
		other.SentAt = now
		g.Check(other) // a fresh message triggers pruning
		if err := g.Check(m); err == nil {
			t.Fatalf("replay accepted %v after first delivery", time.Duration(step+1)*30*time.Second)
		}
	}
}
