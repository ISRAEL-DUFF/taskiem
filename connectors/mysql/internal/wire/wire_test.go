package wire

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// bufConn is a net.Conn over in-memory buffers.
type bufConn struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func (b *bufConn) Read(p []byte) (int, error)       { return b.in.Read(p) }
func (b *bufConn) Write(p []byte) (int, error)      { return b.out.Write(p) }
func (b *bufConn) Close() error                     { return nil }
func (b *bufConn) LocalAddr() net.Addr              { return nil }
func (b *bufConn) RemoteAddr() net.Addr             { return nil }
func (b *bufConn) SetDeadline(time.Time) error      { return nil }
func (b *bufConn) SetReadDeadline(time.Time) error  { return nil }
func (b *bufConn) SetWriteDeadline(time.Time) error { return nil }
func newBufConn(in []byte) (*Conn, *bufConn) {
	bc := &bufConn{in: bytes.NewReader(in)}
	return NewConn(bc), bc
}

func TestLenencMatchesTheDocs(t *testing.T) {
	for _, tc := range []struct {
		v   uint64
		hex string
	}{
		{0, "00"}, {250, "fa"}, {251, "fcfb00"}, {0xffff, "fcffff"},
		{0x10000, "fd000001"}, {0xffffff, "fdffffff"},
		{0x1000000, "fe0000000100000000"},
	} {
		got := AppendLenenc(nil, tc.v)
		if hex.EncodeToString(got) != tc.hex {
			t.Errorf("AppendLenenc(%d) = %x, want %s", tc.v, got, tc.hex)
		}
		v, n, null, err := ReadLenenc(got)
		if err != nil || null || v != tc.v || n != len(got) {
			t.Errorf("ReadLenenc(%x) = %d %d %v %v", got, v, n, null, err)
		}
	}
	if _, _, null, err := ReadLenenc([]byte{0xfb}); !null || err != nil {
		t.Errorf("0xfb is NULL in text rows: %v %v", null, err)
	}
	if _, _, _, err := ReadLenenc([]byte{0xff}); !errors.Is(err, ErrProtocol) {
		t.Errorf("0xff is not a length: %v", err)
	}
	if _, _, _, err := ReadLenenc([]byte{0xfc, 0x01}); !errors.Is(err, ErrProtocol) {
		t.Errorf("truncated length: %v", err)
	}
}

func TestComQuitFraming(t *testing.T) {
	// "01 00 00 00 01": COM_QUIT, from the protocol basics page.
	c, bc := newBufConn(nil)
	if err := c.WritePacket([]byte{ComQuit}); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(bc.out.Bytes()); got != "0100000001" {
		t.Errorf("framing %s", got)
	}
}

func TestLargePacketsSplitAndJoin(t *testing.T) {
	for _, size := range []int{MaxPayload - 1, MaxPayload, MaxPayload + 10, 2 * MaxPayload} {
		payload := bytes.Repeat([]byte{0xab}, size)
		c, bc := newBufConn(nil)
		if err := c.WritePacket(payload); err != nil {
			t.Fatal(err)
		}
		raw := bc.out.Bytes()
		wantPackets := size/MaxPayload + 1
		if want := size + 4*wantPackets; len(raw) != want {
			t.Fatalf("size %d: wrote %d bytes, want %d", size, len(raw), want)
		}
		if size == MaxPayload && !bytes.Equal(raw[len(raw)-4:], []byte{0, 0, 0, 1}) {
			t.Errorf("an exact multiple must end with an empty packet: % x", raw[len(raw)-4:])
		}
		r, _ := newBufConn(raw)
		got, err := r.ReadPacket()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("size %d: read back %d bytes, %v", size, len(got), err)
		}
	}
}

func TestSequenceIDIsChecked(t *testing.T) {
	c, _ := newBufConn(unhex(t, "01000005 00"))
	if _, err := c.ReadPacket(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("out-of-order packet: %v", err)
	}
	if _, err := c.ReadPacket(); !errors.Is(err, ErrBroken) {
		t.Errorf("connection must stay broken: %v", err)
	}
}

func TestMaxReadBoundsMemory(t *testing.T) {
	c, _ := newBufConn(unhex(t, "ffffff00"))
	c.MaxRead = 1024
	if _, err := c.ReadPacket(); !errors.Is(err, ErrConfig) {
		t.Fatalf("oversized message: %v", err)
	}
}

func TestParseHandshakeFromTheDocs(t *testing.T) {
	// The Protocol::HandshakeV10 example (MySQL 5.5.2-m2, no plugin auth).
	p := unhex(t, "0a 35 2e 35 2e 32 2d 6d 32 00 0b 00 00 00 64 76 48 40 49 2d 43 4a 00 ff f7 08 02 00 00 00 00 00 00 00 00 00 00 00 00 00 00 2a 34 64 7c 63 5a 77 6b 34 5e 5d 3a 00")
	h, err := parseHandshake(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.version != "5.5.2-m2" || h.connID != 11 || h.caps != 0xf7ff || h.charset != 8 || h.status != 2 || h.plugin != "" {
		t.Errorf("handshake %+v", h)
	}
	if string(h.scramble) != "dvH@I-CJ*4d|cZwk4^]:" {
		t.Errorf("scramble %q", h.scramble)
	}
}

func TestParseHandshakeWithPlugin(t *testing.T) {
	b := []byte{10}
	b = append(append(b, "8.0.36"...), 0)
	b = append(b, 7, 0, 0, 0)
	b = append(b, "abcdefgh"...)
	b = append(b, 0, 0xff, 0xff, 45, 2, 0, 0xff, 0xdf, 21)
	b = append(b, make([]byte, 10)...)
	b = append(append(b, "ijklmnopqrst"...), 0)
	b = append(b, "caching_sha2_password"...) // no final NUL, as some servers send
	h, err := parseHandshake(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.plugin != PluginCachingSHA2 || string(h.scramble) != "abcdefghijklmnopqrst" || h.caps&capPluginAuth == 0 {
		t.Errorf("handshake %+v", h)
	}
	if _, err := parseHandshake([]byte{9, 'x', 0}); !errors.Is(err, ErrConfig) {
		t.Errorf("protocol 9: %v", err)
	}
	if _, err := parseHandshake(b[:20]); !errors.Is(err, ErrProtocol) {
		t.Errorf("truncated: %v", err)
	}
}

func TestInitialErrPacket(t *testing.T) {
	// A server refusing the connection sends ERR without a SQL state.
	p := append([]byte{0xff, 0x10, 0x04}, "Too many connections"...)
	_, err := parseHandshake(p)
	var se *ServerError
	if !errors.As(err, &se) || se.Code != 1040 || se.SQLState != "" || se.Message != "Too many connections" {
		t.Errorf("%v", err)
	}
}

func TestScramblesKnownAnswers(t *testing.T) {
	// Computed independently (Python hashlib) from the formulas in the docs.
	s := make([]byte, 20)
	for i := range s {
		s[i] = byte(i + 1)
	}
	if got := hex.EncodeToString(NativePassword(s, "secret")); got != "b32bb3a583e1340c0a1108d58b1be49781ad8c2f" {
		t.Errorf("native %s", got)
	}
	if got := hex.EncodeToString(CachingSHA2Password(s, "secret")); got != "746ebe205d56a0707acb3e796e834e0dd7b1d61743b26bd5202c7a623230c7c9" {
		t.Errorf("sha2 %s", got)
	}
	if len(NativePassword(s, "")) != 0 || len(CachingSHA2Password(s, "")) != 0 {
		t.Error("an empty password sends an empty response")
	}
}

func TestOKAndErrPacketsFromTheDocs(t *testing.T) {
	ok, err := parseOK(unhex(t, "00 00 00 02 00 00 00"))
	if err != nil || ok.AffectedRows != 0 || ok.Status != 2 || ok.Warnings != 0 {
		t.Errorf("OK %+v %v", ok, err)
	}
	ok, err = parseOK(unhex(t, "00 fc e8 03 07 22 00 01 00"))
	if err != nil || ok.AffectedRows != 1000 || ok.LastInsertID != 7 || ok.Status != 0x22 || ok.Warnings != 1 {
		t.Errorf("OK %+v %v", ok, err)
	}
	e := parseErrPacket(unhex(t, "ff 48 04 23 48 59 30 30 30 4e 6f 20 74 61 62 6c 65 73 20 75 73 65 64"))
	var se *ServerError
	if !errors.As(e, &se) || se.Code != 1096 || se.SQLState != "HY000" || se.Message != "No tables used" {
		t.Errorf("ERR %v", e)
	}
	if _, err := parseEOF(unhex(t, "fe 00 00 02 00")); err != nil {
		t.Errorf("EOF: %v", err)
	}
	if _, err := parseEOF(unhex(t, "fe 00 00")); !errors.Is(err, ErrProtocol) {
		t.Errorf("short EOF: %v", err)
	}
}

func colDef(name string, typ byte, charset uint16, flags uint16) []byte {
	b := AppendLenencBytes(nil, []byte("def"))
	for _, s := range []string{"db", "t", "t", name, name} {
		b = AppendLenencBytes(b, []byte(s))
	}
	b = append(b, 0x0c, byte(charset), byte(charset>>8), 11, 0, 0, 0, typ, byte(flags), byte(flags>>8), 0, 0, 0)
	return b
}

func TestParseColumn(t *testing.T) {
	col, err := parseColumn(colDef("amount", TypeLongLong, 63, FlagUnsigned|FlagNotNull))
	if err != nil || col.Name != "amount" || col.Type != TypeLongLong || !col.Unsigned() || col.Charset != 63 || col.Length != 11 {
		t.Errorf("%+v %v", col, err)
	}
	bad := colDef("x", TypeLong, 63, 0)
	if _, err := parseColumn(append(bad, 0)); !errors.Is(err, ErrProtocol) {
		t.Errorf("trailing byte: %v", err)
	}
	bad[0] = 3
	copy(bad[1:4], "abc")
	if _, err := parseColumn(bad); !errors.Is(err, ErrProtocol) {
		t.Errorf("catalog must be def: %v", err)
	}
}

func TestBinaryTemporalValuesFromTheDocs(t *testing.T) {
	r := reader{b: unhex(t, "0b da 07 0a 11 13 1b 1e 01 00 00 00")}
	v, err := decodeBinaryDateTime(&r)
	d := v.(DateTime)
	if err != nil || d.String() != "2010-10-17 19:27:30.000001" {
		t.Errorf("datetime %v %v", d, err)
	}
	if tm, ok := d.Time(); !ok || tm.Format(time.RFC3339Nano) != "2010-10-17T19:27:30.000001Z" {
		t.Errorf("as time %v", tm)
	}
	r = reader{b: unhex(t, "0c 01 78 00 00 00 13 1b 1e 01 00 00 00")}
	v, err = decodeBinaryTime(&r)
	if err != nil || v.(Duration).String() != "-2899:27:30.000001" {
		t.Errorf("time %v %v", v, err)
	}
	r = reader{b: []byte{0}}
	v, _ = decodeBinaryDateTime(&r)
	if _, ok := v.(DateTime).Time(); ok || v.(DateTime).String() != "0000-00-00 00:00:00" {
		t.Errorf("zero date %v", v)
	}
	r = reader{b: []byte{5, 1, 2, 3, 4, 5}}
	if _, err := decodeBinaryDateTime(&r); !errors.Is(err, ErrProtocol) {
		t.Errorf("length 5: %v", err)
	}
}

func TestBinaryRowNullBitmapFromTheDocs(t *testing.T) {
	// Nine columns with the ninth NULL: the bitmap is [00] [04] (offset 2).
	cols := make([]Column, 9)
	for i := range cols {
		cols[i] = Column{Name: "c", Type: TypeTiny}
	}
	p := append([]byte{0x00, 0x00, 0x04}, 1, 2, 3, 4, 5, 6, 7, 0xff)
	row, err := decodeBinaryRow(p, cols)
	if err != nil {
		t.Fatal(err)
	}
	if row[8] != nil || row[0] != int64(1) || row[7] != int64(-1) {
		t.Errorf("row %v", row)
	}
	if _, err := decodeBinaryRow(append(p, 9), cols); !errors.Is(err, ErrProtocol) {
		t.Errorf("trailing bytes: %v", err)
	}
	if _, err := decodeBinaryRow(p[:5], cols); !errors.Is(err, ErrProtocol) {
		t.Errorf("short row: %v", err)
	}
}

func TestBinaryRowTypes(t *testing.T) {
	cols := []Column{
		{Type: TypeLongLong, Flags: FlagUnsigned}, {Type: TypeShort}, {Type: TypeLong},
		{Type: TypeDouble}, {Type: TypeFloat}, {Type: TypeNewDecimal}, {Type: TypeVarString}, {Type: TypeYear},
	}
	p := []byte{0x00, 0x00, 0x00}
	p = append(p, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff) // max uint64
	p = append(p, 0xfe, 0xff)                                     // -2
	p = append(p, 0x00, 0x00, 0x00, 0x80)                         // min int32
	p = append(p, 0, 0, 0, 0, 0, 0, 0xf8, 0x3f)                   // 1.5
	p = append(p, 0xcd, 0xcc, 0xcc, 0x3d)                         // 0.1f
	p = append(p, 5, '1', '2', '.', '5', '0')
	p = append(p, 3, 'f', 'o', 'o')
	p = append(p, 0xea, 0x07) // 2026
	row, err := decodeBinaryRow(p, cols)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{uint64(1<<64 - 1), int64(-2), int64(-1 << 31), 1.5, float32(0.1), Decimal("12.50"), []byte("foo"), uint64(2026)}
	for i := range want {
		if b, ok := want[i].([]byte); ok {
			if !bytes.Equal(b, row[i].([]byte)) {
				t.Errorf("col %d: %v", i, row[i])
			}
			continue
		}
		if row[i] != want[i] {
			t.Errorf("col %d: %#v, want %#v", i, row[i], want[i])
		}
	}
	if _, err := decodeBinaryRow([]byte{0, 0, 7}, []Column{{Type: 99}}); !errors.Is(err, ErrProtocol) {
		t.Errorf("unknown type: %v", err)
	}
}

func TestExecutePacketMatchesTheDocs(t *testing.T) {
	// The COM_STMT_EXECUTE example binds "foo" to statement 1 as type 0x0f
	// (VARCHAR); this client sends strings as VAR_STRING (0xfd), otherwise
	// byte for byte the same.
	st := &Stmt{ID: 1, Params: make([]Column, 1)}
	got, err := st.ExecutePacket([]any{"foo"})
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "17 01 00 00 00 00 01 00 00 00 00 01 fd 00 03 66 6f 6f")
	if !bytes.Equal(got, want) {
		t.Errorf("got  % x\nwant % x", got, want)
	}
	if _, err := st.ExecutePacket(nil); !errors.Is(err, ErrConfig) {
		t.Errorf("wrong arity: %v", err)
	}
}

func TestExecutePacketParamTypes(t *testing.T) {
	st := &Stmt{ID: 9, Params: make([]Column, 9)}
	got, err := st.ExecutePacket([]any{nil, int64(-1), uint64(5), 2.5, true, []byte{0, 1}, Decimal("1.10"), time.Date(2026, 10, 5, 10, 0, 0, 1000, time.UTC), "x"})
	if err != nil {
		t.Fatal(err)
	}
	r := reader{b: got[10:]}
	if bm := r.take(2, "bitmap"); bm[0] != 0x01 || bm[1] != 0 {
		t.Errorf("null bitmap % x", bm)
	}
	if r.u8("bound") != 1 {
		t.Error("new_params_bind_flag")
	}
	types := r.take(18, "types")
	wantTypes := []byte{TypeNull, 0, TypeLongLong, 0, TypeLongLong, 0x80, TypeDouble, 0, TypeTiny, 0, TypeBlob, 0, TypeNewDecimal, 0, TypeDateTime, 0, TypeVarString, 0}
	if !bytes.Equal(types, wantTypes) {
		t.Errorf("types % x", types)
	}
	if r.u64("int") != 1<<64-1 || r.u64("uint") != 5 || r.u64("double") != 0x4004000000000000 || r.u8("bool") != 1 {
		t.Error("numeric values")
	}
	if b, _ := r.lenencBytes("blob"); !bytes.Equal(b, []byte{0, 1}) {
		t.Error("blob")
	}
	if b, _ := r.lenencBytes("decimal"); string(b) != "1.10" {
		t.Error("decimal")
	}
	if dt := r.take(12, "datetime"); !bytes.Equal(dt, unhex(t, "0b ea 07 0a 05 0a 00 00 01 00 00 00")) {
		t.Errorf("datetime % x", dt)
	}
	if b, _ := r.lenencBytes("string"); string(b) != "x" || r.done("packet") != nil {
		t.Error("string / trailing")
	}
	if _, err := (&Stmt{Params: make([]Column, 1)}).ExecutePacket([]any{struct{}{}}); !errors.Is(err, ErrConfig) {
		t.Errorf("unsupported type: %v", err)
	}
}

func TestTextRow(t *testing.T) {
	row, err := decodeTextRow([]byte{0xfb, 2, '4', '2'}, 2)
	if err != nil || row[0] != nil || string(row[1].([]byte)) != "42" {
		t.Errorf("%v %v", row, err)
	}
}

func TestContextCancelInterruptsIO(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	c := NewConn(client)
	c.caps = capProtocol41 | capDeprecateEOF
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.Query(ctx, "SELECT SLEEP(100)")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancellation was not prompt")
	}
	if err := c.Ping(context.Background()); !errors.Is(err, ErrBroken) {
		t.Errorf("an interrupted connection must not be reused: %v", err)
	}
}

func TestDeadlineInterruptsIO(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	c := NewConn(client)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Query(ctx, "SELECT 1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}
