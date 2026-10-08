// Package fakeserver is a strict, in-process MySQL server for tests. It
// speaks just enough of the protocol to drive the client through the
// connection phase (both auth plugins, auth switch, TLS, RSA key exchange)
// and prepared statements, and records any protocol violation it sees as a
// test failure. Its encoders and decoders are written separately from the
// client's on purpose, so a shared misunderstanding is less likely to hide.
//
//nolint:gosec // test helper: wire-format truncations are deliberate
package fakeserver

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // protocol-defined
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Capability and type values, spelled out from the protocol documentation
// rather than imported from the client.
const (
	cLongPassword  = 1 << 0
	cFoundRows     = 1 << 1
	cLongFlag      = 1 << 2
	cConnectWithDB = 1 << 3
	cLocalFiles    = 1 << 7
	cProtocol41    = 1 << 9
	cSSL           = 1 << 11
	cTransactions  = 1 << 13
	cSecureConn    = 1 << 15
	cMultiStmts    = 1 << 16
	cMultiResults  = 1 << 17
	cPSMulti       = 1 << 18
	cPluginAuth    = 1 << 19
	cConnectAttrs  = 1 << 20
	cAuthLenenc    = 1 << 21
	cSessionTrack  = 1 << 23
	cDeprecateEOF  = 1 << 24
	cQueryAttrs    = 1 << 27
)

// Column types used by tests.
const (
	TDecimal    = 0
	TTiny       = 1
	TShort      = 2
	TLong       = 3
	TFloat      = 4
	TDouble     = 5
	TNull       = 6
	TTimestamp  = 7
	TLongLong   = 8
	TInt24      = 9
	TDate       = 10
	TTime       = 11
	TDateTime   = 12
	TYear       = 13
	TVarchar    = 15
	TBit        = 16
	TJSON       = 245
	TNewDecimal = 246
	TEnum       = 247
	TBlob       = 252
	TVarString  = 253
	TString     = 254
)

// Column describes a result column.
type Column struct {
	Name     string
	Type     byte
	Unsigned bool
	Binary   bool // charset 63 (VARBINARY, BLOB, JSON...)
}

// DT is a DATE/DATETIME/TIMESTAMP value to send.
type DT struct {
	Y               uint16
	Mo, D, H, Mi, S uint8
	Us              uint32
}

// TM is a TIME value to send.
type TM struct {
	Neg     bool
	Days    uint32
	H, M, S uint8
	Us      uint32
}

// Error is an ERR packet to send.
type Error struct {
	Code  uint16
	State string
	Msg   string
}

// Result scripts the server's answer to one SQL string.
type Result struct {
	Params  int
	Columns []Column
	Rows    [][]any
	// Without Columns the statement answers OK with these counts.
	AffectedRows, LastInsertID uint64
	// Write statements fail with error 1792 inside a read-only transaction.
	Write      bool
	Err        *Error // sent in answer to execute
	PrepareErr *Error // sent in answer to prepare
	// Check inspects the decoded parameters; an error is a test failure.
	Check func(args []any) error
	// DropOnPrepare / DropOnExecute close the connection on receipt.
	DropOnPrepare, DropOnExecute bool
	// Hang never answers execute.
	Hang bool
}

// Server is a fake MySQL server. Set fields before Start.
type Server struct {
	Version  string // default "8.0.36"
	User     string
	Password string
	Database string
	// DefaultPlugin is announced in the handshake (default
	// caching_sha2_password); AccountPlugin is what the account uses
	// (default DefaultPlugin). When they differ the server switches.
	DefaultPlugin, AccountPlugin string
	// FullAuth makes caching_sha2_password miss its cache.
	FullAuth bool
	TLS      *tls.Config
	// RequireTLS refuses plaintext logins (like require_secure_transport).
	RequireTLS bool
	// NoDeprecateEOF stops the server offering CLIENT_DEPRECATE_EOF.
	NoDeprecateEOF bool
	Statements     map[string]*Result

	t  testing.TB
	ln net.Listener
	*state
}

// state is allocated by Start, so a Server can be copied before then.
type state struct {
	key  *rsa.PrivateKey
	mu   sync.Mutex
	log  []string
	wg   sync.WaitGroup
	open map[net.Conn]bool
}

// Start listens on loopback and returns the address.
func (s *Server) Start(t testing.TB) string {
	t.Helper()
	s.t = t
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	s.state = &state{open: map[net.Conn]bool{}}
	if s.Version == "" {
		s.Version = "8.0.36"
	}
	if s.DefaultPlugin == "" {
		s.DefaultPlugin = "caching_sha2_password"
	}
	if s.AccountPlugin == "" {
		s.AccountPlugin = s.DefaultPlugin
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.open[nc] = true
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() {
					_ = nc.Close()
					s.mu.Lock()
					delete(s.open, nc)
					s.mu.Unlock()
				}()
				(&session{s: s, nc: nc, br: bufio.NewReader(nc), stmts: map[uint32]*prepared{}}).serve()
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		for c := range s.open {
			_ = c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return ln.Addr().String()
}

// Log returns what the server saw, one line per command.
func (s *Server) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

// Connections counts open server-side connections.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

func (s *Server) record(format string, a ...any) {
	s.mu.Lock()
	s.log = append(s.log, fmt.Sprintf(format, a...))
	s.mu.Unlock()
}

type prepared struct {
	sql string
	res *Result
}

type session struct {
	s        *Server
	nc       net.Conn
	br       *bufio.Reader
	seq      byte
	caps     uint32
	tls      bool
	scramble []byte

	sessionRO, inTx, txRO bool
	stmts                 map[uint32]*prepared
	nextID                uint32
}

// violation is a client protocol mistake.
type violation struct{ msg string }

func (v violation) Error() string { return v.msg }

func (c *session) bad(format string, a ...any) {
	panic(violation{fmt.Sprintf(format, a...)})
}

func (c *session) serve() {
	defer func() {
		if r := recover(); r != nil {
			if v, ok := r.(violation); ok {
				c.s.t.Errorf("fakeserver: client protocol violation: %s", v.msg)
				return
			}
			if _, ok := r.(ioDone); ok {
				return
			}
			panic(r)
		}
	}()
	if !c.login() {
		return
	}
	for {
		c.seq = 0
		p, ok := c.readOrEOF()
		if !ok {
			return
		}
		if len(p) == 0 {
			c.bad("empty command packet")
		}
		if !c.command(p) {
			return
		}
	}
}

// ioDone unwinds a session whose peer went away.
type ioDone struct{}

func (c *session) readOrEOF() ([]byte, bool) {
	var payload []byte
	for {
		var h [4]byte
		if _, err := io.ReadFull(c.br, h[:]); err != nil {
			if payload == nil {
				return nil, false
			}
			panic(ioDone{})
		}
		n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
		if h[3] != c.seq {
			c.bad("sequence id %d, expected %d", h[3], c.seq)
		}
		c.seq++
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.br, buf); err != nil {
			panic(ioDone{})
		}
		payload = append(payload, buf...)
		if n < 0xffffff {
			if payload == nil {
				payload = []byte{}
			}
			return payload, true
		}
	}
}

func (c *session) read() []byte {
	p, ok := c.readOrEOF()
	if !ok {
		panic(ioDone{})
	}
	return p
}

func (c *session) write(p []byte) {
	for {
		n := len(p)
		if n > 0xffffff {
			n = 0xffffff
		}
		hdr := []byte{byte(n), byte(n >> 8), byte(n >> 16), c.seq}
		c.seq++
		if _, err := c.nc.Write(append(hdr, p[:n]...)); err != nil {
			panic(ioDone{})
		}
		p = p[n:]
		if n < 0xffffff {
			return
		}
	}
}

func lenenc(b []byte, v uint64) []byte {
	if v < 251 {
		return append(b, byte(v))
	}
	if v < 1<<16 {
		return append(b, 0xfc, byte(v), byte(v>>8))
	}
	if v < 1<<24 {
		return append(b, 0xfd, byte(v), byte(v>>8), byte(v>>16))
	}
	b = append(b, 0xfe)
	return binary.LittleEndian.AppendUint64(b, v)
}

func lenencStr(b []byte, s string) []byte { return append(lenenc(b, uint64(len(s))), s...) }

// cursor reads client payloads, failing the test on any short read.
type cursor struct {
	c    *session
	b    []byte
	what string
}

func (r *cursor) n(k int, field string) []byte {
	if len(r.b) < k {
		r.c.bad("%s: truncated at %s", r.what, field)
	}
	v := r.b[:k]
	r.b = r.b[k:]
	return v
}
func (r *cursor) u8(f string) byte    { return r.n(1, f)[0] }
func (r *cursor) u16(f string) uint16 { return binary.LittleEndian.Uint16(r.n(2, f)) }
func (r *cursor) u32(f string) uint32 { return binary.LittleEndian.Uint32(r.n(4, f)) }
func (r *cursor) u64(f string) uint64 { return binary.LittleEndian.Uint64(r.n(8, f)) }
func (r *cursor) lenenc(f string) uint64 {
	switch x := r.u8(f); {
	case x < 0xfb:
		return uint64(x)
	case x == 0xfc:
		return uint64(r.u16(f))
	case x == 0xfd:
		b := r.n(3, f)
		return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16
	case x == 0xfe:
		return r.u64(f)
	default:
		r.c.bad("%s: bad length-encoded integer prefix 0x%x at %s", r.what, x, f)
	}
	return 0
}
func (r *cursor) lenencBytes(f string) []byte {
	n := r.lenenc(f)
	if n > uint64(len(r.b)) {
		r.c.bad("%s: %s length %d exceeds packet", r.what, f, n)
	}
	return r.n(int(n), f)
}
func (r *cursor) nul(f string) string {
	i := bytes.IndexByte(r.b, 0)
	if i < 0 {
		r.c.bad("%s: %s is not NUL-terminated", r.what, f)
	}
	s := string(r.b[:i])
	r.b = r.b[i+1:]
	return s
}
func (r *cursor) end() {
	if len(r.b) != 0 {
		r.c.bad("%s: %d trailing bytes (% x)", r.what, len(r.b), r.b)
	}
}

func (c *session) sendErr(e Error) {
	b := []byte{0xff}
	b = binary.LittleEndian.AppendUint16(b, e.Code)
	b = append(b, '#')
	b = append(b, e.State...)
	b = append(b, e.Msg...)
	c.write(b)
}

func (c *session) status() uint16 {
	st := uint16(0x0002) // autocommit
	if c.inTx {
		st = 0x0001
	}
	return st
}

func (c *session) sendOK(affected, insertID uint64) {
	b := []byte{0x00}
	b = lenenc(b, affected)
	b = lenenc(b, insertID)
	b = binary.LittleEndian.AppendUint16(b, c.status())
	b = binary.LittleEndian.AppendUint16(b, 0)
	c.write(b)
}

// endRows ends a result set: OK with a 0xFE header, or a legacy EOF.
func (c *session) endRows() {
	if c.caps&cDeprecateEOF != 0 {
		b := []byte{0xfe, 0, 0}
		b = binary.LittleEndian.AppendUint16(b, c.status())
		b = binary.LittleEndian.AppendUint16(b, 0)
		c.write(b)
		return
	}
	c.eof()
}

func (c *session) eof() {
	b := []byte{0xfe, 0, 0}
	b = binary.LittleEndian.AppendUint16(b, c.status())
	c.write(b)
}

func (s *Server) serverCaps() uint32 {
	caps := uint32(cLongPassword | cFoundRows | cLongFlag | cConnectWithDB | cLocalFiles | cProtocol41 |
		cTransactions | cSecureConn | cMultiStmts | cMultiResults | cPSMulti | cPluginAuth |
		cConnectAttrs | cAuthLenenc | cSessionTrack | cQueryAttrs)
	if !s.NoDeprecateEOF {
		caps |= cDeprecateEOF
	}
	if s.TLS != nil {
		caps |= cSSL
	}
	return caps
}

func newScramble() []byte {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	for i := range b { // printable, never NUL, as real servers do
		b[i] = b[i]%94 + 33
	}
	return b
}

func (c *session) login() bool {
	s := c.s
	c.scramble = newScramble()
	caps := s.serverCaps()
	h := []byte{10}
	h = append(append(h, s.Version...), 0)
	h = binary.LittleEndian.AppendUint32(h, 42)
	h = append(h, c.scramble[:8]...)
	h = append(h, 0)
	h = binary.LittleEndian.AppendUint16(h, uint16(caps))
	h = append(h, 255)
	h = binary.LittleEndian.AppendUint16(h, 0x0002)
	h = binary.LittleEndian.AppendUint16(h, uint16(caps>>16))
	h = append(h, 21)
	h = append(h, make([]byte, 10)...)
	h = append(h, c.scramble[8:]...)
	h = append(h, 0)
	h = append(append(h, s.DefaultPlugin...), 0)
	c.write(h)

	p := c.read()
	if len(p) == 32 { // SSLRequest
		r := cursor{c: c, b: p, what: "SSLRequest"}
		cc := r.u32("capabilities")
		if cc&cSSL == 0 {
			c.bad("32-byte packet without CLIENT_SSL")
		}
		if s.TLS == nil {
			c.bad("SSLRequest though the server did not offer TLS")
		}
		if r.u32("max packet") == 0 {
			c.bad("SSLRequest max packet size is zero")
		}
		r.u8("charset")
		if !bytes.Equal(r.n(23, "filler"), make([]byte, 23)) {
			c.bad("SSLRequest filler is not zero")
		}
		// The client's ClientHello may already sit in our read buffer.
		tc := tls.Server(bufferedConn{c.nc, c.br}, s.TLS)
		if err := tc.Handshake(); err != nil {
			s.record("tls handshake failed: %v", err)
			return false
		}
		c.nc, c.br, c.tls = tc, bufio.NewReader(tc), true
		p = c.read()
		if binary.LittleEndian.Uint32(p)&cSSL == 0 {
			c.bad("handshake response after TLS lacks CLIENT_SSL")
		}
	}
	r := cursor{c: c, b: p, what: "HandshakeResponse41"}
	c.caps = r.u32("capabilities")
	if c.caps&^caps != 0 {
		c.bad("client claims capabilities the server lacks: %#x", c.caps&^caps)
	}
	for _, f := range []struct {
		bit  uint32
		name string
	}{{cLocalFiles, "LOCAL_FILES"}, {cMultiStmts, "MULTI_STATEMENTS"}, {cQueryAttrs, "QUERY_ATTRIBUTES"}, {cSessionTrack, "SESSION_TRACK"}} {
		if c.caps&f.bit != 0 {
			c.bad("client must not enable %s", f.name)
		}
	}
	if c.caps&cProtocol41 == 0 || c.caps&cSecureConn == 0 || c.caps&cPluginAuth == 0 {
		c.bad("client lacks PROTOCOL_41/SECURE_CONNECTION/PLUGIN_AUTH")
	}
	if c.caps&cSSL != 0 && !c.tls {
		c.bad("CLIENT_SSL set without an SSLRequest")
	}
	if r.u32("max packet") == 0 {
		c.bad("max packet size is zero")
	}
	if r.u8("charset") == 0 {
		c.bad("charset is zero")
	}
	if !bytes.Equal(r.n(23, "filler"), make([]byte, 23)) {
		c.bad("filler is not zero")
	}
	user := r.nul("user")
	var auth []byte
	if c.caps&cAuthLenenc != 0 {
		auth = append([]byte(nil), r.lenencBytes("auth response")...)
	} else {
		auth = append([]byte(nil), r.n(int(r.u8("auth length")), "auth response")...)
	}
	db := ""
	if c.caps&cConnectWithDB != 0 {
		db = r.nul("database")
	}
	plugin := r.nul("plugin name")
	attrs := map[string]string{}
	if c.caps&cConnectAttrs != 0 {
		ar := cursor{c: c, b: r.lenencBytes("attributes"), what: "connect attributes"}
		for len(ar.b) > 0 {
			k := string(ar.lenencBytes("key"))
			attrs[k] = string(ar.lenencBytes("value"))
		}
	}
	r.end()
	s.record("login user=%s db=%s plugin=%s tls=%v attrs=%v", user, db, plugin, c.tls, attrs)

	if s.RequireTLS && !c.tls {
		c.sendErr(Error{3159, "HY000", "Connections using insecure transport are prohibited while --require_secure_transport=ON."})
		return false
	}
	if user != s.User || (s.Database != "" && db != s.Database) {
		c.sendErr(Error{1045, "28000", "Access denied for user '" + user + "'"})
		return false
	}
	nonce := c.scramble
	if plugin != s.AccountPlugin {
		nonce = newScramble()
		sw := []byte{0xfe}
		sw = append(append(sw, s.AccountPlugin...), 0)
		sw = append(append(sw, nonce...), 0)
		c.write(sw)
		auth = c.read()
		s.record("auth switch to %s", s.AccountPlugin)
	}
	if !c.verify(s.AccountPlugin, nonce, auth) {
		c.sendErr(Error{1045, "28000", "Access denied for user '" + user + "' (using password: YES)"})
		return false
	}
	c.sendOK(0, 0)
	return true
}

func (c *session) verify(plugin string, nonce, auth []byte) bool {
	pw := c.s.Password
	switch plugin {
	case "mysql_native_password":
		if pw == "" {
			return len(auth) == 0
		}
		if len(auth) != 20 {
			return false
		}
		// The server keeps SHA1(SHA1(pw)) and recovers SHA1(pw) from the reply.
		s1 := sha1.Sum([]byte(pw))                                         //nolint:gosec // protocol-defined
		stored := sha1.Sum(s1[:])                                          //nolint:gosec // protocol-defined
		h := sha1.Sum(append(append([]byte(nil), nonce...), stored[:]...)) //nolint:gosec // protocol-defined
		cand := make([]byte, 20)
		for i := range cand {
			cand[i] = auth[i] ^ h[i]
		}
		got := sha1.Sum(cand) //nolint:gosec // protocol-defined
		return got == stored
	case "caching_sha2_password":
		if !c.s.FullAuth {
			if pw == "" {
				if len(auth) != 0 {
					return false
				}
			} else {
				if len(auth) != 32 {
					return false
				}
				m1 := sha256.Sum256([]byte(pw))
				stored := sha256.Sum256(m1[:])
				h := sha256.Sum256(append(append([]byte(nil), stored[:]...), nonce...))
				cand := make([]byte, 32)
				for i := range cand {
					cand[i] = auth[i] ^ h[i]
				}
				if sha256.Sum256(cand) != stored {
					return false
				}
			}
			c.write([]byte{0x01, 0x03})
			return true
		}
		c.write([]byte{0x01, 0x04})
		p := c.read()
		if c.tls {
			return string(p) == pw+"\x00"
		}
		if !bytes.Equal(p, []byte{0x02}) {
			c.bad("caching_sha2 full auth without TLS: expected public key request 0x02, got % x", p)
		}
		c.s.mu.Lock()
		if c.s.key == nil {
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				c.s.mu.Unlock()
				c.bad("rsa: %v", err)
			}
			c.s.key = k
		}
		key := c.s.key
		c.s.mu.Unlock()
		der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
		c.write(append([]byte{0x01}, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})...))
		enc := c.read()
		plain, err := rsa.DecryptOAEP(sha1.New(), nil, key, enc, nil) //nolint:gosec // protocol-defined
		if err != nil {
			c.bad("RSA-OAEP decrypt of password failed: %v", err)
		}
		for i := range plain {
			plain[i] ^= nonce[i%len(nonce)]
		}
		c.s.record("rsa password exchange")
		return string(plain) == pw+"\x00"
	case "mysql_clear_password":
		if !c.tls {
			c.bad("cleartext password sent without TLS")
		}
		return string(auth) == pw+"\x00"
	}
	return false
}

func (c *session) command(p []byte) bool {
	switch p[0] {
	case 0x01: // COM_QUIT
		if len(p) != 1 {
			c.bad("COM_QUIT with payload")
		}
		c.s.record("quit")
		return false
	case 0x0e: // COM_PING
		c.sendOK(0, 0)
	case 0x03:
		c.query(string(p[1:]))
	case 0x16:
		return c.prepare(string(p[1:]))
	case 0x17:
		return c.execute(p)
	case 0x19:
		if len(p) != 5 {
			c.bad("COM_STMT_CLOSE length %d", len(p))
		}
		id := binary.LittleEndian.Uint32(p[1:])
		if c.stmts[id] == nil {
			c.bad("COM_STMT_CLOSE of unknown statement %d", id)
		}
		delete(c.stmts, id)
		c.s.record("close %d", id)
	default:
		c.bad("unexpected command 0x%02x", p[0])
	}
	return true
}

func (c *session) query(sql string) {
	c.s.record("query: %s", sql)
	up := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case up == "SET SESSION TRANSACTION READ ONLY":
		if c.inTx {
			c.sendErr(Error{1568, "25001", "Transaction characteristics can't be changed while a transaction is in progress"})
			return
		}
		c.sessionRO = true
	case up == "START TRANSACTION READ ONLY":
		c.inTx, c.txRO = true, true
	case up == "START TRANSACTION" || up == "BEGIN":
		c.inTx, c.txRO = true, false
	case up == "COMMIT" || up == "ROLLBACK":
		c.inTx, c.txRO = false, false
	case strings.HasPrefix(up, "SET "):
	default:
		res := c.s.Statements[sql]
		if res == nil {
			c.sendErr(Error{1064, "42000", "You have an error in your SQL syntax near '" + sql + "'"})
			return
		}
		if res.Err != nil {
			c.sendErr(*res.Err)
			return
		}
		if res.Columns == nil {
			c.sendOK(res.AffectedRows, res.LastInsertID)
			return
		}
		c.write(lenenc(nil, uint64(len(res.Columns))))
		for _, col := range res.Columns {
			c.write(colDef(col))
		}
		if c.caps&cDeprecateEOF == 0 {
			c.eof()
		}
		for _, row := range res.Rows {
			var b []byte
			for _, v := range row {
				if v == nil {
					b = append(b, 0xfb)
				} else {
					b = lenencStr(b, fmt.Sprint(v))
				}
			}
			c.write(b)
		}
		c.endRows()
		return
	}
	c.sendOK(0, 0)
}

func colDef(col Column) []byte {
	b := lenencStr(nil, "def")
	b = lenencStr(b, "testdb")
	b = lenencStr(b, "t")
	b = lenencStr(b, "t")
	b = lenencStr(b, col.Name)
	b = lenencStr(b, col.Name)
	b = append(b, 0x0c)
	charset := uint16(255)
	var flags uint16
	switch col.Type {
	case TTiny, TShort, TLong, TLongLong, TInt24, TYear, TFloat, TDouble, TDecimal, TNewDecimal,
		TDate, TTime, TDateTime, TTimestamp, TBit, TNull:
		charset = 63
	}
	if col.Binary || col.Type == TJSON {
		charset = 63
	}
	if charset == 63 {
		flags |= 0x0080
	}
	if col.Unsigned {
		flags |= 0x0020
	}
	b = binary.LittleEndian.AppendUint16(b, charset)
	b = binary.LittleEndian.AppendUint32(b, 255)
	b = append(b, col.Type)
	b = binary.LittleEndian.AppendUint16(b, flags)
	b = append(b, 0, 0, 0)
	return b
}

func (c *session) prepare(sql string) bool {
	c.s.record("prepare: %s", sql)
	res := c.s.Statements[sql]
	if res == nil {
		c.sendErr(Error{1064, "42000", "You have an error in your SQL syntax near '" + sql + "'"})
		return true
	}
	if res.DropOnPrepare {
		return false
	}
	if res.PrepareErr != nil {
		c.sendErr(*res.PrepareErr)
		return true
	}
	c.nextID++
	c.stmts[c.nextID] = &prepared{sql: sql, res: res}
	b := []byte{0x00}
	b = binary.LittleEndian.AppendUint32(b, c.nextID)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(res.Columns)))
	b = binary.LittleEndian.AppendUint16(b, uint16(res.Params))
	b = append(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)
	c.write(b)
	if res.Params > 0 {
		for range res.Params {
			c.write(colDef(Column{Name: "?", Type: TVarString, Binary: true}))
		}
		if c.caps&cDeprecateEOF == 0 {
			c.eof()
		}
	}
	if len(res.Columns) > 0 {
		for _, col := range res.Columns {
			c.write(colDef(col))
		}
		if c.caps&cDeprecateEOF == 0 {
			c.eof()
		}
	}
	return true
}

func (c *session) execute(p []byte) bool {
	r := cursor{c: c, b: p[1:], what: "COM_STMT_EXECUTE"}
	id := r.u32("statement id")
	st := c.stmts[id]
	if st == nil {
		c.bad("execute of unknown statement %d", id)
	}
	if f := r.u8("flags"); f != 0 {
		c.bad("cursor flags %d; the client should not open cursors", f)
	}
	if n := r.u32("iteration count"); n != 1 {
		c.bad("iteration count %d", n)
	}
	res := st.res
	args := make([]any, res.Params)
	if res.Params > 0 {
		nulls := r.n((res.Params+7)/8, "null bitmap")
		if r.u8("new params bound") != 1 {
			c.bad("new_params_bind_flag must be 1")
		}
		types := make([][2]byte, res.Params)
		for i := range types {
			t := r.n(2, "parameter type")
			types[i] = [2]byte{t[0], t[1]}
			if t[1]&^0x80 != 0 {
				c.bad("parameter %d type flags byte 0x%x", i, t[1])
			}
		}
		for i := range args {
			isNull := nulls[i/8]&(1<<(i%8)) != 0
			t, unsigned := types[i][0], types[i][1]&0x80 != 0
			if t == TNull {
				if !isNull {
					c.bad("parameter %d typed NULL but not in the null bitmap", i)
				}
				continue
			}
			if isNull {
				c.bad("parameter %d in the null bitmap but typed %d", i, t)
			}
			switch t {
			case TTiny:
				if unsigned {
					args[i] = uint64(r.u8("tiny"))
				} else {
					args[i] = int64(int8(r.u8("tiny")))
				}
			case TLongLong:
				v := r.u64("longlong")
				if unsigned {
					args[i] = v
				} else {
					args[i] = int64(v)
				}
			case TDouble:
				args[i] = math.Float64frombits(r.u64("double"))
			case TVarString, TString, TVarchar:
				args[i] = string(r.lenencBytes("string"))
			case TBlob:
				args[i] = append([]byte(nil), r.lenencBytes("blob")...)
			case TNewDecimal:
				args[i] = "decimal:" + string(r.lenencBytes("decimal"))
			case TDateTime, TTimestamp, TDate:
				n := r.u8("datetime length")
				if n != 0 && n != 4 && n != 7 && n != 11 {
					c.bad("datetime parameter length %d", n)
				}
				d := DT{}
				if n >= 4 {
					d.Y, d.Mo, d.D = r.u16("year"), r.u8("month"), r.u8("day")
				}
				if n >= 7 {
					d.H, d.Mi, d.S = r.u8("hour"), r.u8("minute"), r.u8("second")
				}
				if n == 11 {
					d.Us = r.u32("microsecond")
				}
				args[i] = d
			default:
				c.bad("parameter %d has unexpected type %d", i, t)
			}
		}
	}
	r.end()
	c.s.record("execute: %s %v", st.sql, args)
	if res.Check != nil {
		if err := res.Check(args); err != nil {
			c.bad("parameters for %q: %v", st.sql, err)
		}
	}
	if res.DropOnExecute {
		return false
	}
	if res.Hang {
		_, _ = io.Copy(io.Discard, c.br) // until the client hangs up
		return false
	}
	if res.Write && (c.sessionRO || c.txRO) {
		c.sendErr(Error{1792, "25006", "Cannot execute statement in a READ ONLY transaction."})
		return true
	}
	if res.Err != nil {
		c.sendErr(*res.Err)
		return true
	}
	if res.Columns == nil {
		c.sendOK(res.AffectedRows, res.LastInsertID)
		return true
	}
	c.write(lenenc(nil, uint64(len(res.Columns))))
	for _, col := range res.Columns {
		c.write(colDef(col))
	}
	if c.caps&cDeprecateEOF == 0 {
		c.eof()
	}
	for _, row := range res.Rows {
		c.write(binaryRow(res.Columns, row))
	}
	c.endRows()
	return true
}

func binaryRow(cols []Column, row []any) []byte {
	nulls := make([]byte, (len(cols)+9)/8)
	var vals []byte
	le := binary.LittleEndian
	for i, col := range cols {
		v := row[i]
		if v == nil {
			nulls[(i+2)/8] |= 1 << ((i + 2) % 8)
			continue
		}
		switch col.Type {
		case TTiny:
			vals = append(vals, byte(asU64(v)))
		case TShort, TYear:
			vals = le.AppendUint16(vals, uint16(asU64(v)))
		case TLong, TInt24:
			vals = le.AppendUint32(vals, uint32(asU64(v)))
		case TLongLong:
			vals = le.AppendUint64(vals, asU64(v))
		case TFloat:
			vals = le.AppendUint32(vals, math.Float32bits(v.(float32)))
		case TDouble:
			vals = le.AppendUint64(vals, math.Float64bits(v.(float64)))
		case TDate, TDateTime, TTimestamp:
			d := v.(DT)
			switch {
			case d == DT{}:
				vals = append(vals, 0)
			case d.H == 0 && d.Mi == 0 && d.S == 0 && d.Us == 0:
				vals = append(vals, 4)
				vals = le.AppendUint16(vals, d.Y)
				vals = append(vals, d.Mo, d.D)
			case d.Us == 0:
				vals = append(vals, 7)
				vals = le.AppendUint16(vals, d.Y)
				vals = append(vals, d.Mo, d.D, d.H, d.Mi, d.S)
			default:
				vals = append(vals, 11)
				vals = le.AppendUint16(vals, d.Y)
				vals = append(vals, d.Mo, d.D, d.H, d.Mi, d.S)
				vals = le.AppendUint32(vals, d.Us)
			}
		case TTime:
			t := v.(TM)
			if t == (TM{}) {
				vals = append(vals, 0)
			} else {
				n := byte(8)
				if t.Us != 0 {
					n = 12
				}
				neg := byte(0)
				if t.Neg {
					neg = 1
				}
				vals = append(vals, n, neg)
				vals = le.AppendUint32(vals, t.Days)
				vals = append(vals, t.H, t.M, t.S)
				if n == 12 {
					vals = le.AppendUint32(vals, t.Us)
				}
			}
		default:
			switch s := v.(type) {
			case string:
				vals = lenencStr(vals, s)
			case []byte:
				vals = lenencStr(vals, string(s))
			default:
				panic(fmt.Sprintf("fakeserver: column %s needs string or []byte, got %T", col.Name, v))
			}
		}
	}
	return append(append([]byte{0x00}, nulls...), vals...)
}

func asU64(v any) uint64 {
	switch n := v.(type) {
	case int64:
		return uint64(n)
	case uint64:
		return n
	case int:
		return uint64(n)
	}
	panic(fmt.Sprintf("fakeserver: integer column needs int64/uint64, got %T", v))
}

// TLSConfig makes a self-signed certificate for host and returns the
// server config and the PEM to trust it.
func TLSConfig(host string) (*tls.Config, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          bigOne(),
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	if len(der) == 0 {
		return nil, nil, errors.New("empty certificate")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func bigOne() *big.Int { return big.NewInt(1) }

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
