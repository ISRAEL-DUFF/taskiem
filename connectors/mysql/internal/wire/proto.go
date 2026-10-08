// Package wire is a clean-room client for the MySQL client/server protocol,
// written from Oracle's public protocol documentation
// (https://dev.mysql.com/doc/dev/mysql-server/latest/PAGE_PROTOCOL.html)
// and cross-checked against MariaDB's public protocol pages. It covers what
// the Taskiem connector needs: the connection phase (Handshake v10,
// HandshakeResponse41, TLS upgrade, the mysql_native_password,
// caching_sha2_password and mysql_clear_password plugins, auth switch),
// COM_QUERY with text result sets, and prepared statements with binary
// parameters and binary result rows.
//
// It deliberately never advertises CLIENT_LOCAL_FILES (so the server cannot
// ask to read local files) nor CLIENT_MULTI_STATEMENTS (so one statement
// string cannot smuggle a second one).
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Capability flags (Capabilities Flags page).
const (
	capLongPassword     uint32 = 1 << 0
	capFoundRows        uint32 = 1 << 1
	capLongFlag         uint32 = 1 << 2
	capConnectWithDB    uint32 = 1 << 3
	capLocalFiles       uint32 = 1 << 7
	capProtocol41       uint32 = 1 << 9
	capSSL              uint32 = 1 << 11
	capTransactions     uint32 = 1 << 13
	capSecureConnection uint32 = 1 << 15
	capMultiStatements  uint32 = 1 << 16
	capMultiResults     uint32 = 1 << 17
	capPSMultiResults   uint32 = 1 << 18
	capPluginAuth       uint32 = 1 << 19
	capConnectAttrs     uint32 = 1 << 20
	capPluginAuthLenenc uint32 = 1 << 21
	capSessionTrack     uint32 = 1 << 23
	capDeprecateEOF     uint32 = 1 << 24
	capQueryAttributes  uint32 = 1 << 27
)

// Exported capability bits, for tests and fakes.
const (
	CapFoundRows        = capFoundRows
	CapConnectWithDB    = capConnectWithDB
	CapLocalFiles       = capLocalFiles
	CapProtocol41       = capProtocol41
	CapSSL              = capSSL
	CapSecureConnection = capSecureConnection
	CapMultiStatements  = capMultiStatements
	CapMultiResults     = capMultiResults
	CapPSMultiResults   = capPSMultiResults
	CapPluginAuth       = capPluginAuth
	CapConnectAttrs     = capConnectAttrs
	CapPluginAuthLenenc = capPluginAuthLenenc
	CapSessionTrack     = capSessionTrack
	CapDeprecateEOF     = capDeprecateEOF
	CapQueryAttributes  = capQueryAttributes
	CapTransactions     = capTransactions
	CapLongPassword     = capLongPassword
	CapLongFlag         = capLongFlag
)

// Server status flags.
const (
	StatusInTrans          uint16 = 0x0001
	StatusAutocommit       uint16 = 0x0002
	StatusMoreResultsExist uint16 = 0x0008
)

// Command bytes.
const (
	ComQuit        byte = 0x01
	ComQuery       byte = 0x03
	ComPing        byte = 0x0e
	ComStmtPrepare byte = 0x16
	ComStmtExecute byte = 0x17
	ComStmtClose   byte = 0x19
)

// Field types (enum_field_types).
const (
	TypeDecimal    byte = 0
	TypeTiny       byte = 1
	TypeShort      byte = 2
	TypeLong       byte = 3
	TypeFloat      byte = 4
	TypeDouble     byte = 5
	TypeNull       byte = 6
	TypeTimestamp  byte = 7
	TypeLongLong   byte = 8
	TypeInt24      byte = 9
	TypeDate       byte = 10
	TypeTime       byte = 11
	TypeDateTime   byte = 12
	TypeYear       byte = 13
	TypeNewDate    byte = 14
	TypeVarchar    byte = 15
	TypeBit        byte = 16
	TypeTimestamp2 byte = 17
	TypeDateTime2  byte = 18
	TypeTime2      byte = 19
	TypeVector     byte = 242
	TypeJSON       byte = 245
	TypeNewDecimal byte = 246
	TypeEnum       byte = 247
	TypeSet        byte = 248
	TypeTinyBlob   byte = 249
	TypeMediumBlob byte = 250
	TypeLongBlob   byte = 251
	TypeBlob       byte = 252
	TypeVarString  byte = 253
	TypeString     byte = 254
	TypeGeometry   byte = 255
)

// Column flags.
const (
	FlagNotNull  uint16 = 0x0001
	FlagBinary   uint16 = 0x0080
	FlagUnsigned uint16 = 0x0020
)

// CharsetBinary is the "binary" collation id: byte strings, not text.
const CharsetBinary uint16 = 63

// charsetUTF8MB4 is utf8mb4_general_ci, known to MySQL 5.5.3+ and MariaDB.
const charsetUTF8MB4 = 45

// MaxPayload is the largest payload one packet carries; longer payloads
// are split across packets.
const MaxPayload = 1<<24 - 1

var (
	// ErrProtocol marks a malformed or unexpected message: the stream can
	// no longer be trusted and the connection is closed.
	ErrProtocol = errors.New("mysql protocol error")
	// ErrBroken is returned by any call on a connection that already failed.
	ErrBroken = errors.New("mysql connection is broken")
	// ErrConfig marks a problem the server or settings make permanent
	// (TLS refused, an unsupported auth plugin, a bad argument count).
	ErrConfig = errors.New("mysql configuration error")
)

// ServerError is an ERR packet.
type ServerError struct {
	Code     uint16
	SQLState string
	Message  string
}

func (e *ServerError) Error() string {
	if e.SQLState == "" {
		return fmt.Sprintf("mysql error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("mysql error %d (%s): %s", e.Code, e.SQLState, e.Message)
}

func protoErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, a...))
}

// reader decodes protocol data types from one payload. The first short
// read sets a sticky error.
type reader struct {
	b   []byte
	err error
}

func (r *reader) fail(what string) {
	if r.err == nil {
		r.err = protoErr("truncated %s", what)
	}
	r.b = nil
}

func (r *reader) take(n int, what string) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.b) < n {
		r.fail(what)
		return nil
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) u8(what string) byte {
	b := r.take(1, what)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) u16(what string) uint16 {
	b := r.take(2, what)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) u24(what string) uint32 {
	b := r.take(3, what)
	if b == nil {
		return 0
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

func (r *reader) u32(what string) uint32 {
	b := r.take(4, what)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) u64(what string) uint64 {
	b := r.take(8, what)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

// lenenc reads a length-encoded integer. null reports the 0xFB marker that
// text rows use for NULL.
func (r *reader) lenenc(what string) (v uint64, null bool) {
	first := r.u8(what)
	if r.err != nil {
		return 0, false
	}
	switch {
	case first < 0xfb:
		return uint64(first), false
	case first == 0xfb:
		return 0, true
	case first == 0xfc:
		return uint64(r.u16(what)), false
	case first == 0xfd:
		return uint64(r.u24(what)), false
	case first == 0xfe:
		return r.u64(what), false
	}
	r.err = protoErr("invalid length-encoded integer prefix 0xff in %s", what)
	r.b = nil
	return 0, false
}

func (r *reader) lenencBytes(what string) (b []byte, null bool) {
	n, null := r.lenenc(what)
	if null || r.err != nil {
		return nil, null
	}
	if n > uint64(len(r.b)) {
		r.fail(what)
		return nil, false
	}
	return r.take(int(n), what), false // #nosec G115 -- bounded by len(r.b) above
}

func (r *reader) lenencString(what string) string {
	b, null := r.lenencBytes(what)
	if null && r.err == nil {
		r.err = protoErr("unexpected NULL in %s", what)
	}
	return string(b)
}

// nulString reads a NUL-terminated string.
func (r *reader) nulString(what string) string {
	if r.err != nil {
		return ""
	}
	for i, c := range r.b {
		if c == 0 {
			s := string(r.b[:i])
			r.b = r.b[i+1:]
			return s
		}
	}
	r.fail(what + " terminator")
	return ""
}

func (r *reader) rest() []byte {
	v := r.b
	r.b = nil
	return v
}

// done reports trailing bytes after a message that should have ended.
func (r *reader) done(what string) error {
	if r.err == nil && len(r.b) != 0 {
		r.err = protoErr("%d unexpected trailing bytes after %s", len(r.b), what)
	}
	return r.err
}

// AppendLenenc appends a length-encoded integer.
func AppendLenenc(b []byte, v uint64) []byte {
	switch {
	case v < 0xfb:
		return append(b, byte(v)) // #nosec G115 -- v < 0xfb
	case v <= 0xffff:
		return append(b, 0xfc, byte(v), byte(v>>8)) // #nosec G115 -- little-endian bytes of a bounded value
	case v <= 0xffffff:
		return append(b, 0xfd, byte(v), byte(v>>8), byte(v>>16)) // #nosec G115 -- little-endian bytes of a bounded value
	}
	b = append(b, 0xfe)
	return binary.LittleEndian.AppendUint64(b, v)
}

// AppendLenencBytes appends a length-encoded string.
func AppendLenencBytes(b, s []byte) []byte {
	return append(AppendLenenc(b, uint64(len(s))), s...)
}

// ReadLenenc decodes a length-encoded integer from the front of b and
// returns it with the number of bytes used; null reports 0xFB.
func ReadLenenc(b []byte) (v uint64, n int, null bool, err error) {
	r := reader{b: b}
	v, null = r.lenenc("length-encoded integer")
	return v, len(b) - len(r.b), null, r.err
}

func parseErrPacket(p []byte) error {
	r := reader{b: p[1:]}
	e := &ServerError{Code: r.u16("error code")}
	if len(r.b) > 0 && r.b[0] == '#' {
		r.take(1, "sql state marker")
		e.SQLState = string(r.take(5, "sql state"))
	}
	e.Message = string(r.rest())
	if r.err != nil {
		return r.err
	}
	return e
}

// Result is what an OK packet (or the end of a result set) reports.
type Result struct {
	AffectedRows uint64
	LastInsertID uint64
	Status       uint16
	Warnings     uint16
	Info         string
}

func parseOK(p []byte) (Result, error) {
	r := reader{b: p[1:]}
	var res Result
	res.AffectedRows, _ = r.lenenc("affected rows")
	res.LastInsertID, _ = r.lenenc("last insert id")
	res.Status = r.u16("status flags")
	res.Warnings = r.u16("warnings")
	// Without CLIENT_SESSION_TRACK the rest is human-readable info.
	res.Info = string(r.rest())
	return res, r.err
}

// parseEOF reads a pre-DEPRECATE_EOF EOF packet.
func parseEOF(p []byte) (Result, error) {
	if len(p) != 5 || p[0] != 0xfe {
		return Result{}, protoErr("malformed EOF packet (% x)", p)
	}
	r := reader{b: p[1:]}
	var res Result
	res.Warnings = r.u16("warnings")
	res.Status = r.u16("status flags")
	return res, r.err
}
