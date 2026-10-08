package wire

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// DefaultMaxRead bounds the size of one logical message read from the
// server (a row, for instance), so a huge value cannot exhaust memory.
const DefaultMaxRead = 64 << 20

// Conn is one client connection. It is not safe for concurrent use.
type Conn struct {
	nc     net.Conn
	br     *bufio.Reader
	seq    byte
	broken bool

	caps   uint32 // negotiated capabilities
	status uint16 // last server status flags
	tls    bool

	// MaxRead caps one logical message; DefaultMaxRead when zero.
	MaxRead int

	ServerVersion string
	ConnectionID  uint32
}

// NewConn wraps nc without performing the handshake; for packet-level
// tests. Use Connect for a usable connection.
func NewConn(nc net.Conn) *Conn {
	return &Conn{nc: nc, br: bufio.NewReaderSize(nc, 16<<10)}
}

// Capabilities are the capabilities in force after the handshake.
func (c *Conn) Capabilities() uint32 { return c.caps }

// Status is the server status from the last OK or EOF packet.
func (c *Conn) Status() uint16 { return c.status }

// TLS reports whether the connection is encrypted.
func (c *Conn) TLS() bool { return c.tls }

// MariaDB reports whether the server identifies as MariaDB.
func (c *Conn) MariaDB() bool { return strings.Contains(strings.ToLower(c.ServerVersion), "mariadb") }

// ResetSeq starts a new command: the sequence id goes back to zero.
func (c *Conn) ResetSeq() { c.seq = 0 }

func (c *Conn) maxRead() int {
	if c.MaxRead > 0 {
		return c.MaxRead
	}
	return DefaultMaxRead
}

// ReadPacket reads one logical payload, joining packets split at
// MaxPayload, and checks every sequence id.
func (c *Conn) ReadPacket() ([]byte, error) {
	if c.broken {
		return nil, ErrBroken
	}
	var payload []byte
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
			c.broken = true
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("read packet header: connection closed by server: %w", io.ErrUnexpectedEOF)
			}
			return nil, fmt.Errorf("read packet header: %w", err)
		}
		n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
		if hdr[3] != c.seq {
			c.broken = true
			return nil, protoErr("packet sequence id %d, expected %d", hdr[3], c.seq)
		}
		c.seq++
		if len(payload)+n > c.maxRead() {
			c.broken = true
			return nil, fmt.Errorf("%w: server message exceeds %d bytes", ErrConfig, c.maxRead())
		}
		start := len(payload)
		payload = append(payload, make([]byte, n)...)
		if _, err := io.ReadFull(c.br, payload[start:]); err != nil {
			c.broken = true
			return nil, fmt.Errorf("read packet body: %w", err)
		}
		if n < MaxPayload {
			if payload == nil {
				payload = []byte{}
			}
			return payload, nil
		}
	}
}

// WritePacket writes payload, splitting it into MaxPayload chunks; a
// payload that is an exact multiple of MaxPayload ends with an empty packet.
func (c *Conn) WritePacket(payload []byte) error {
	if c.broken {
		return ErrBroken
	}
	for {
		n := min(len(payload), MaxPayload)
		buf := make([]byte, 4, 4+n)
		buf[0], buf[1], buf[2], buf[3] = byte(n), byte(n>>8), byte(n>>16), c.seq // #nosec G115 -- n <= MaxPayload, 3 bytes
		buf = append(buf, payload[:n]...)
		if _, err := c.nc.Write(buf); err != nil {
			c.broken = true
			return fmt.Errorf("write packet: %w", err)
		}
		c.seq++
		payload = payload[n:]
		if n < MaxPayload {
			return nil
		}
	}
}

// longAgo is a deadline in the past, used to interrupt blocked I/O.
var longAgo = time.Unix(1, 0)

// run executes f under ctx: ctx's deadline becomes the socket deadline and
// cancelling ctx interrupts blocked I/O. An interrupted connection is broken.
func (c *Conn) run(ctx context.Context, f func() error) error {
	if c.broken {
		return ErrBroken
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dl, _ := ctx.Deadline()
	if err := c.nc.SetDeadline(dl); err != nil {
		c.broken = true
		return fmt.Errorf("set deadline: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = c.nc.SetDeadline(longAgo) })
	err := f()
	if !stop() {
		// ctx ended while f ran; the socket deadline is now in the past.
		c.broken = true
		if err != nil {
			return fmt.Errorf("%w (%w)", ctx.Err(), err)
		}
	}
	var ne net.Error
	if err != nil && errors.As(err, &ne) && ne.Timeout() {
		c.broken = true
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("%w (%w)", cerr, err)
		}
		if !dl.IsZero() {
			// The socket deadline is ctx's; it can fire a moment before ctx.Done.
			return fmt.Errorf("%w (%w)", context.DeadlineExceeded, err)
		}
	}
	return err
}

// Abort closes the socket at once, without COM_QUIT. The server rolls back
// any open transaction.
func (c *Conn) Abort() error {
	c.broken = true
	return c.nc.Close()
}

// Close sends COM_QUIT when the connection is healthy, then closes it.
func (c *Conn) Close() error {
	if !c.broken {
		_ = c.nc.SetDeadline(time.Now().Add(time.Second))
		c.ResetSeq()
		_ = c.WritePacket([]byte{ComQuit})
	}
	c.broken = true
	return c.nc.Close()
}
