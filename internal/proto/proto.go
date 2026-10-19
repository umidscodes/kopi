// Package proto is Kopi's wire format: key derivation, authenticated encryption,
// length-prefixed framing with a hard size limit, and replay protection.
//
// Sealed message layout:
//
//	version (1) | nonce (24) | XChaCha20-Poly1305 ciphertext of the encoded Message
//
// The version byte is Version (2) and is authenticated as additional data. Open
// rejects any other version (including v0.1's 1) with ErrBadVersion before decrypting.
//
// Encoded Message layout (inside the ciphertext, big endian):
//
//	id (16) | sentAt unix ms (8) | flags (1) | len(from) (1) | from | text
//
// flags: bit 0 = sensitive. Unused bits are written as zero and ignored on Open.
package proto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// MaxTextBytes is the largest clip Kopi will send or accept (SPEC.md, Feature: Size limit).
	MaxTextBytes = 15 * 1024 * 1024
	// MaxFromBytes caps the sender device ID.
	MaxFromBytes = 255

	// Version is the protocol version, the first byte of every sealed message.
	// v0.1 used 1; version 2 adds the flags byte. The two cannot exchange messages.
	Version = 2

	flagSensitive = 1 << 0

	headerLen   = 16 + 8 + 1 + 1
	sealedExtra = 1 + chacha20poly1305.NonceSizeX + chacha20poly1305.Overhead
	// MaxFrameBytes is the largest frame on the wire: a max-size clip plus all overhead.
	MaxFrameBytes = MaxTextBytes + headerLen + MaxFromBytes + sealedExtra
)

var (
	ErrTooLarge   = errors.New("kopi: clip exceeds 15 MB limit")
	ErrDecrypt    = errors.New("kopi: cannot decrypt message (passphrase mismatch or tampering)")
	ErrBadVersion = errors.New("kopi: unsupported protocol version")
	ErrMalformed  = errors.New("kopi: malformed message")
)

// Fixed, app-specific salt. All devices sharing a passphrase must derive the same
// key, so the salt can't be random; Argon2id's memory cost carries the weight.
var keySalt = []byte("kopi/v1/clipboard-sync-key")

// Key is the symmetric key shared by every device with the same passphrase.
type Key [chacha20poly1305.KeySize]byte

// DeriveKey turns a passphrase into a Key with Argon2id (64 MiB, 3 passes).
// It is deliberately slow (~100ms); call it once at startup, not per message.
func DeriveKey(passphrase string) Key {
	var k Key
	copy(k[:], argon2.IDKey([]byte(passphrase), keySalt, 3, 64*1024, 4, uint32(len(k))))
	return k
}

// Message is one clip as it travels between devices.
type Message struct {
	ID     [16]byte
	From   string // sender device ID
	SentAt time.Time
	Text   string
	// Sensitive marks a clip the receiver must never list (SPEC.md RECENT-19).
	Sensitive bool
}

// NewID returns a random message ID.
func NewID() [16]byte {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(fmt.Sprintf("kopi: crypto/rand failed: %v", err))
	}
	return id
}

// Seal encodes and encrypts m.
func Seal(key Key, m Message) ([]byte, error) {
	if len(m.Text) > MaxTextBytes {
		return nil, ErrTooLarge
	}
	if len(m.From) > MaxFromBytes {
		return nil, fmt.Errorf("%w: sender id too long", ErrMalformed)
	}
	plain := make([]byte, 0, headerLen+len(m.From)+len(m.Text))
	plain = append(plain, m.ID[:]...)
	plain = binary.BigEndian.AppendUint64(plain, uint64(m.SentAt.UnixMilli()))
	var flags byte
	if m.Sensitive {
		flags |= flagSensitive
	}
	plain = append(plain, flags, byte(len(m.From)))
	plain = append(plain, m.From...)
	plain = append(plain, m.Text...)

	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+aead.NonceSize(), sealedExtra+len(plain))
	out[0] = Version
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	nonce := out[1:]
	// The version byte is authenticated as additional data.
	return aead.Seal(out, nonce, plain, out[:1]), nil
}

// Open decrypts and decodes a sealed message. A wrong key returns ErrDecrypt.
func Open(key Key, sealed []byte) (Message, error) {
	if len(sealed) < sealedExtra {
		return Message{}, ErrMalformed
	}
	if sealed[0] != Version {
		return Message{}, ErrBadVersion
	}
	aead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return Message{}, err
	}
	nonce := sealed[1 : 1+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, sealed[1+aead.NonceSize():], sealed[:1])
	if err != nil {
		return Message{}, ErrDecrypt
	}
	if len(plain) < headerLen {
		return Message{}, ErrMalformed
	}
	var m Message
	copy(m.ID[:], plain[:16])
	m.SentAt = time.UnixMilli(int64(binary.BigEndian.Uint64(plain[16:24])))
	m.Sensitive = plain[24]&flagSensitive != 0
	fromLen := int(plain[25])
	rest := plain[headerLen:]
	if len(rest) < fromLen {
		return Message{}, ErrMalformed
	}
	m.From = string(rest[:fromLen])
	if len(rest)-fromLen > MaxTextBytes {
		return Message{}, ErrTooLarge
	}
	m.Text = string(rest[fromLen:])
	return m, nil
}

// WriteFrame writes payload with a 4-byte big-endian length prefix.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameBytes {
		return ErrTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one frame. An oversized length is rejected before any of the
// body is read or allocated (SPEC.md SIZE-2).
func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameBytes {
		return nil, ErrTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
