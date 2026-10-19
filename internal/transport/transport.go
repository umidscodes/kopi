// Package transport moves sealed messages between devices over TCP and keeps track
// of known peers and their state.
//
// One message per connection: the sender writes one frame, the receiver answers with
// a single ack byte and closes. Clips are rare, so connection reuse isn't worth it.
package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/umidscodes/kopi/internal/proto"
)

const (
	ackOK       byte = 0
	ackMismatch byte = 1 // receiver could not decrypt: different passphrase
	ackRejected byte = 2 // receiver decrypted but refused (replay, stale, too large, …)

	// IOTimeout bounds every read and write. A 15 MB clip on a slow LAN fits easily.
	IOTimeout   = 30 * time.Second
	DialTimeout = 3 * time.Second
	// MaxConns caps concurrent inbound connections so a flood can't exhaust memory
	// (each may buffer up to proto.MaxFrameBytes).
	MaxConns = 8
)

var (
	ErrPeerMismatch = errors.New("kopi: peer uses a different passphrase")
	ErrRejected     = errors.New("kopi: peer rejected the message")
)

// Handler processes one received sealed message. remote is the sender's IP.
// Returning an error wrapping proto.ErrDecrypt tells the sender "passphrase mismatch".
type Handler func(remote netip.Addr, sealed []byte) error

// Server accepts messages from peers.
type Server struct {
	ln       net.Listener
	handle   Handler
	sem      chan struct{}
	identity atomic.Pointer[[]byte] // JSON answer to a hello; nil: don't answer
}

// Identity is what a node reveals to anyone who asks: the same fields mDNS publishes.
type Identity struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"v"`
}

// helloMagic is a request frame asking "who are you?". Sealed messages always start
// with proto.Version (2), never 'K', so the two can never be confused.
var helloMagic = []byte("KOPI?")

const (
	ackHello     byte = 3 // followed by one frame of Identity JSON
	maxHelloSize      = 1024
)

// SetIdentity makes the server answer hello requests (used by tailnet discovery).
func (s *Server) SetIdentity(id Identity) {
	b, _ := json.Marshal(id)
	s.identity.Store(&b)
}

// Listen opens a TCP listener on addr (e.g. ":47800", or "127.0.0.1:0" in tests).
func Listen(addr string, h Handler) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, handle: h, sem: make(chan struct{}, MaxConns)}, nil
}

// Addr is the address the server is listening on.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Port is the TCP port the server is listening on.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// Serve accepts connections until ctx is done.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.ln.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.serveConn(conn)
			}()
		default:
			conn.Close() // too busy; the sender will see the peer as unreachable this once
		}
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(IOTimeout))
	var remote netip.Addr
	if ap, err := netip.ParseAddrPort(conn.RemoteAddr().String()); err == nil {
		remote = ap.Addr().Unmap()
	}
	sealed, err := proto.ReadFrame(conn)
	if err != nil {
		if errors.Is(err, proto.ErrTooLarge) {
			conn.Write([]byte{ackRejected})
		}
		return
	}
	if bytes.Equal(sealed, helloMagic) {
		if id := s.identity.Load(); id != nil {
			conn.Write([]byte{ackHello})
			proto.WriteFrame(conn, *id)
		}
		return
	}
	ack := ackOK
	if err := s.handle(remote, sealed); err != nil {
		ack = ackRejected
		if errors.Is(err, proto.ErrDecrypt) {
			ack = ackMismatch
		}
	}
	conn.Write([]byte{ack})
}

// Hello asks the node at addr who it is. It fails if nothing answers or the answer
// isn't a Kopi identity, so it doubles as a "is Kopi running there?" check.
func Hello(ctx context.Context, addr string) (Identity, error) {
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Identity{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(DialTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	if err := proto.WriteFrame(conn, helloMagic); err != nil {
		return Identity{}, err
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return Identity{}, err
	}
	if ack[0] != ackHello {
		return Identity{}, errors.New("kopi: not a hello answer")
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return Identity{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxHelloSize {
		return Identity{}, proto.ErrTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(body, &id); err != nil || id.ID == "" {
		return Identity{}, errors.New("kopi: bad hello answer")
	}
	return id, nil
}

// Send delivers one sealed message to addr ("host:port") and waits for the ack.
func Send(ctx context.Context, addr string, sealed []byte) error {
	d := net.Dialer{Timeout: DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(IOTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	if err := proto.WriteFrame(conn, sealed); err != nil {
		return err
	}
	var ack [1]byte
	if _, err := conn.Read(ack[:]); err != nil {
		return err
	}
	switch ack[0] {
	case ackOK:
		return nil
	case ackMismatch:
		return ErrPeerMismatch
	default:
		return ErrRejected
	}
}
