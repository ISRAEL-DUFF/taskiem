package forms

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Scanner checks a file for malware before a submission is accepted
// (forms 6.5). Which scanner Taskiem uses is an open decision (S0); a
// ClamAV daemon run by the operator, with no network of its own, is what
// ClamdScanner speaks to. Without one, forms that ask for files cannot be
// deployed.
type Scanner interface {
	// Scan reports a threat's name, or "" when the file is clean.
	Scan(ctx context.Context, data []byte) (threat string, err error)
}

// ErrNoScanner refuses files where no scanner is configured.
var ErrNoScanner = errors.New("forms: no malware scanner is configured (TASKIEM_CLAMD_ADDR), so forms cannot take files")

// ClamdScanner streams files to clamd (INSTREAM) at Addr (host:port).
type ClamdScanner struct {
	Addr    string
	Timeout time.Duration
}

const clamdChunk = 64 << 10

// Scan sends data in chunks and reads clamd's verdict.
func (c *ClamdScanner) Scan(ctx context.Context, data []byte) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return "", fmt.Errorf("forms: the scanner: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", err
	}
	var size [4]byte
	for off := 0; off < len(data); off += clamdChunk {
		end := min(off+clamdChunk, len(data))
		binary.BigEndian.PutUint32(size[:], uint32(end-off)) //nolint:gosec // at most clamdChunk
		if _, err := conn.Write(size[:]); err != nil {
			return "", err
		}
		if _, err := conn.Write(data[off:end]); err != nil {
			return "", err
		}
	}
	binary.BigEndian.PutUint32(size[:], 0)
	if _, err := conn.Write(size[:]); err != nil {
		return "", err
	}
	reply, err := bufio.NewReader(conn).ReadString(0)
	if err != nil && reply == "" {
		return "", fmt.Errorf("forms: the scanner did not answer: %w", err)
	}
	return parseClamd(strings.TrimRight(reply, "\x00\n"))
}

// parseClamd reads "stream: OK", "stream: <name> FOUND" or an error.
func parseClamd(reply string) (string, error) {
	r := strings.TrimSpace(strings.TrimPrefix(reply, "stream:"))
	switch {
	case r == "OK":
		return "", nil
	case strings.HasSuffix(r, " FOUND"):
		return strings.TrimSuffix(r, " FOUND"), nil
	}
	return "", fmt.Errorf("forms: the scanner answered %q", reply)
}
