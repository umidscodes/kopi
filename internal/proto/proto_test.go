package proto

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
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

// sealRaw encrypts plain exactly as a sender of the given protocol version would,
// authenticating the version byte. Tests use it to build frames Seal won't produce.
func sealRaw(t *testing.T, key Key, ver byte, plain []byte) []byte {
	t.Helper()
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 1+aead.NonceSize())
	out[0] = ver
	if _, err := rand.Read(out[1:]); err != nil {
		t.Fatal(err)
	}
	return aead.Seal(out, out[1:], plain, out[:1])
}

// v2Plain encodes m in the v2 layout with an explicit flags byte.
func v2Plain(m Message, flags byte) []byte {
	p := append([]byte{}, m.ID[:]...)
	p = binary.BigEndian.AppendUint64(p, uint64(m.SentAt.UnixMilli()))
	p = append(p, flags, byte(len(m.From)))
	p = append(p, m.From...)
	return append(p, m.Text...)
}

func TestRECENT19_SensitiveFlagRoundTrips(t *testing.T) {
	for _, sensitive := range []bool{true, false} {
		in := msg("hunter2")
		in.Sensitive = sensitive
		sealed, err := Seal(keyA, in)
		if err != nil {
			t.Fatal(err)
		}
		if sealed[0] != Version || Version != 2 {
			t.Fatalf("first byte = %d, Version = %d; want 2", sealed[0], Version)
		}
		out, err := Open(keyA, sealed)
		if err != nil {
			t.Fatal(err)
		}
		if out.Sensitive != sensitive || out.ID != in.ID || out.From != in.From ||
			!out.SentAt.Equal(in.SentAt) || out.Text != in.Text {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
		}
	}
}

func TestRECENT19_UnusedFlagBitsIgnored(t *testing.T) {
	in := msg("x")
	for _, flags := range []byte{0xFE, 0xFF} {
		out, err := Open(keyA, sealRaw(t, keyA, Version, v2Plain(in, flags)))
		if err != nil {
			t.Fatalf("flags %#x: %v", flags, err)
		}
		if want := flags&1 == 1; out.Sensitive != want || out.Text != "x" || out.From != in.From {
			t.Fatalf("flags %#x: got %+v", flags, out)
		}
	}
}

func TestRECENT20_Version1FrameRejected(t *testing.T) {
	// v0.1 layout: id (16) | sentAt (8) | len(from) (1) | from | text, version byte 1.
	in := msg("from old")
	plain := append([]byte{}, in.ID[:]...)
	plain = binary.BigEndian.AppendUint64(plain, uint64(in.SentAt.UnixMilli()))
	plain = append(plain, byte(len(in.From)))
	plain = append(plain, in.From...)
	plain = append(plain, in.Text...)
	if _, err := Open(keyA, sealRaw(t, keyA, 1, plain)); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("v1 frame with same key: got %v, want ErrBadVersion", err)
	}
	// Any other version is rejected before decrypting, whatever the key.
	for _, ver := range []byte{0, 1, 3, 255} {
		for _, k := range []Key{keyA, keyB} {
			if _, err := Open(keyA, sealRaw(t, k, ver, v2Plain(in, 0))); !errors.Is(err, ErrBadVersion) {
				t.Fatalf("version %d: got %v, want ErrBadVersion", ver, err)
			}
		}
	}
}

func TestRECENT22_SensitiveFlagIsAuthenticated(t *testing.T) {
	in := msg("hunter2")
	in.Sensitive = true
	sealed, err := Seal(keyA, in)
	if err != nil {
		t.Fatal(err)
	}
	// The flags byte sits after id (16) and sentAt (8) in the plaintext; the stream
	// cipher keeps offsets, so flip its bit 0 in the ciphertext.
	flagAt := 1 + chacha20poly1305.NonceSizeX + 16 + 8
	sealed[flagAt] ^= 1
	if _, err := Open(keyA, sealed); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered flag: got %v, want ErrDecrypt", err)
	}
}
