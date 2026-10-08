package wire

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

// Column is a Column Definition (Protocol::ColumnDefinition41).
type Column struct {
	Schema, Table, OrgTable, Name, OrgName string
	Charset                                uint16
	Length                                 uint32
	Type                                   byte
	Flags                                  uint16
	Decimals                               byte
}

// Unsigned reports the UNSIGNED flag.
func (col Column) Unsigned() bool { return col.Flags&FlagUnsigned != 0 }

func parseColumn(p []byte) (Column, error) {
	r := reader{b: p}
	var col Column
	if cat := r.lenencString("catalog"); r.err == nil && cat != "def" {
		return col, protoErr("column catalog %q, expected \"def\"", cat)
	}
	col.Schema = r.lenencString("schema")
	col.Table = r.lenencString("table")
	col.OrgTable = r.lenencString("org_table")
	col.Name = r.lenencString("name")
	col.OrgName = r.lenencString("org_name")
	if n, _ := r.lenenc("fixed fields length"); r.err == nil && n != 0x0c {
		return col, protoErr("column fixed-length fields are %d bytes, expected 12", n)
	}
	col.Charset = r.u16("character set")
	col.Length = r.u32("column length")
	col.Type = r.u8("type")
	col.Flags = r.u16("flags")
	col.Decimals = r.u8("decimals")
	r.take(2, "filler")
	// COM_FIELD_LIST would append a default value; we never send it.
	return col, r.done("column definition")
}

func (c *Conn) deprecateEOF() bool { return c.caps&capDeprecateEOF != 0 }

// readColumns reads n column definitions and, without DEPRECATE_EOF, the
// EOF packet that closes them.
func (c *Conn) readColumns(n uint64) ([]Column, error) {
	if n > 4096 {
		return nil, protoErr("%d columns exceeds MySQL's limit", n)
	}
	cols := make([]Column, 0, n)
	for range n {
		p, err := c.ReadPacket()
		if err != nil {
			return nil, err
		}
		if len(p) > 0 && p[0] == 0xff {
			return nil, c.serverErrAndBreak(p)
		}
		col, err := parseColumn(p)
		if err != nil {
			c.broken = true
			return nil, err
		}
		cols = append(cols, col)
	}
	if n > 0 && !c.deprecateEOF() {
		p, err := c.ReadPacket()
		if err != nil {
			return nil, err
		}
		res, err := parseEOF(p)
		if err != nil {
			c.broken = true
			return nil, err
		}
		c.status = res.Status
	}
	return cols, nil
}

// serverErrAndBreak parses an ERR that arrived where the protocol does not
// allow one, and marks the stream unusable.
func (c *Conn) serverErrAndBreak(p []byte) error {
	c.broken = true
	return parseErrPacket(p)
}

// readResultHeader reads the first packet of a command response: OK, ERR,
// or the column count and definitions of a result set.
func (c *Conn) readResultHeader() (cols []Column, ok *Result, err error) {
	p, err := c.ReadPacket()
	if err != nil {
		return nil, nil, err
	}
	if len(p) == 0 {
		c.broken = true
		return nil, nil, protoErr("empty response")
	}
	switch p[0] {
	case 0x00:
		res, err := parseOK(p)
		if err != nil {
			c.broken = true
			return nil, nil, err
		}
		c.status = res.Status
		return nil, &res, nil
	case 0xff:
		return nil, nil, parseErrPacket(p)
	case 0xfb:
		// LOCAL INFILE request. We never offer CLIENT_LOCAL_FILES, so a
		// server asking for a client file is misbehaving: drop it.
		c.broken = true
		return nil, nil, protoErr("server requested a local file (LOAD DATA LOCAL), which this client never allows")
	}
	r := reader{b: p}
	n, _ := r.lenenc("column count")
	if err := r.done("column count"); err != nil {
		c.broken = true
		return nil, nil, err
	}
	if n == 0 {
		c.broken = true
		return nil, nil, protoErr("result set with zero columns")
	}
	cols, err = c.readColumns(n)
	return cols, nil, err
}

// isTerminator reports the packet that ends a result set's rows: an EOF,
// or with DEPRECATE_EOF an OK packet with the 0xFE header. A text row
// cannot start with 0xFE unless it is at least 16 MB long.
func (c *Conn) isTerminator(p []byte) bool {
	if len(p) == 0 || p[0] != 0xfe {
		return false
	}
	if c.deprecateEOF() {
		return len(p) < MaxPayload
	}
	return len(p) < 9
}

func (c *Conn) parseTerminator(p []byte) (Result, error) {
	if c.deprecateEOF() {
		return parseOK(p)
	}
	return parseEOF(p)
}

// Rows streams one result set and then drains whatever follows it.
type Rows struct {
	c       *Conn
	ctx     context.Context
	cols    []Column
	binary  bool
	inRows  bool // reading rows of the current result set
	first   bool // still on the first result set
	res     Result
	closed  bool
	closeEr error
}

// Columns are the first result set's columns; empty when the statement
// returned only an OK packet.
func (r *Rows) Columns() []Column {
	if !r.first {
		return nil
	}
	return r.cols
}

// Result is the last OK or end-of-rows status; complete after Close.
func (r *Rows) Result() Result { return r.res }

// Next returns the next row of the first result set, or io.EOF.
func (r *Rows) Next() ([]any, error) {
	if !r.first || !r.inRows {
		return nil, io.EOF
	}
	var row []any
	err := r.c.run(r.ctx, func() error {
		var err error
		row, err = r.readRow()
		return err
	})
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, io.EOF
	}
	return row, nil
}

// readRow reads one row of the current result set; nil at its end.
func (r *Rows) readRow() ([]any, error) {
	c := r.c
	p, err := c.ReadPacket()
	if err != nil {
		return nil, err
	}
	if len(p) > 0 && p[0] == 0xff {
		r.inRows = false
		return nil, parseErrPacket(p)
	}
	if c.isTerminator(p) {
		res, err := c.parseTerminator(p)
		if err != nil {
			c.broken = true
			return nil, err
		}
		c.status = res.Status
		r.res.Status, r.res.Warnings = res.Status, res.Warnings
		r.inRows = false
		return nil, nil
	}
	var row []any
	if r.binary {
		row, err = decodeBinaryRow(p, r.cols)
	} else {
		row, err = decodeTextRow(p, len(r.cols))
	}
	if err != nil {
		c.broken = true
		return nil, err
	}
	return row, nil
}

// Close drains the remaining rows and any further result sets (from a
// CALL). It returns the first server error met while draining.
func (r *Rows) Close() error {
	if r.closed {
		return r.closeEr
	}
	r.closed = true
	r.closeEr = r.c.run(r.ctx, func() error {
		for {
			for r.inRows {
				if _, err := r.readRow(); err != nil {
					return err
				}
			}
			if r.c.status&StatusMoreResultsExist == 0 {
				return nil
			}
			r.first = false
			cols, ok, err := r.c.readResultHeader()
			if err != nil {
				return err
			}
			if ok != nil {
				r.res = *ok
				continue
			}
			r.cols, r.inRows = cols, true
		}
	})
	return r.closeEr
}

func (c *Conn) startRows(ctx context.Context, binary bool, cols []Column, ok *Result) *Rows {
	r := &Rows{c: c, ctx: ctx, binary: binary, first: true, cols: cols, inRows: ok == nil}
	if ok != nil {
		r.res = *ok
	}
	return r
}

// Query sends COM_QUERY (the text protocol). Use it for session statements;
// user values belong in prepared statements.
func (c *Conn) Query(ctx context.Context, sql string) (*Rows, error) {
	var cols []Column
	var ok *Result
	err := c.run(ctx, func() error {
		c.ResetSeq()
		if err := c.WritePacket(append([]byte{ComQuery}, sql...)); err != nil {
			return err
		}
		var err error
		cols, ok, err = c.readResultHeader()
		return err
	})
	if err != nil {
		return nil, err
	}
	return c.startRows(ctx, false, cols, ok), nil
}

// Exec runs a statement with COM_QUERY and discards any rows.
func (c *Conn) Exec(ctx context.Context, sql string) (Result, error) {
	rows, err := c.Query(ctx, sql)
	if err != nil {
		return Result{}, err
	}
	err = rows.Close()
	return rows.Result(), err
}

// Stmt is a server-side prepared statement.
type Stmt struct {
	c       *Conn
	ID      uint32
	Params  []Column
	Columns []Column
}

// Prepare sends COM_STMT_PREPARE.
func (c *Conn) Prepare(ctx context.Context, sql string) (*Stmt, error) {
	var st *Stmt
	err := c.run(ctx, func() error {
		c.ResetSeq()
		if err := c.WritePacket(append([]byte{ComStmtPrepare}, sql...)); err != nil {
			return err
		}
		p, err := c.ReadPacket()
		if err != nil {
			return err
		}
		if len(p) > 0 && p[0] == 0xff {
			return parseErrPacket(p)
		}
		if len(p) < 12 || p[0] != 0x00 {
			c.broken = true
			return protoErr("malformed COM_STMT_PREPARE_OK (% x)", p)
		}
		r := reader{b: p[1:]}
		st = &Stmt{c: c, ID: r.u32("statement id")}
		nCols := r.u16("column count")
		nParams := r.u16("parameter count")
		if r.u8("reserved") != 0 {
			c.broken = true
			return protoErr("COM_STMT_PREPARE_OK reserved byte is not zero")
		}
		if len(r.b) > 0 {
			r.u16("warning count")
		}
		// CLIENT_OPTIONAL_RESULTSET_METADATA is never requested, so no
		// metadata_follows byte; anything else is a framing error.
		if err := r.done("COM_STMT_PREPARE_OK"); err != nil {
			c.broken = true
			return err
		}
		if st.Params, err = c.readColumns(uint64(nParams)); err != nil {
			return err
		}
		st.Columns, err = c.readColumns(uint64(nCols))
		return err
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// NumParams is the number of ? placeholders.
func (s *Stmt) NumParams() int { return len(s.Params) }

// ExecutePacket builds the COM_STMT_EXECUTE payload for args.
func (s *Stmt) ExecutePacket(args []any) ([]byte, error) {
	if len(args) != len(s.Params) {
		return nil, fmt.Errorf("%w: statement has %d parameters, got %d values", ErrConfig, len(s.Params), len(args))
	}
	b := []byte{ComStmtExecute}
	b = binary.LittleEndian.AppendUint32(b, s.ID)
	b = append(b, 0x00)                        // CURSOR_TYPE_NO_CURSOR
	b = binary.LittleEndian.AppendUint32(b, 1) // iteration count
	if len(args) == 0 {
		return b, nil
	}
	nullAt := len(b)
	b = append(b, make([]byte, (len(args)+7)/8)...)
	b = append(b, 1) // new_params_bind_flag
	types := len(b)
	b = append(b, make([]byte, 2*len(args))...)
	for i, a := range args {
		t, unsigned, val, err := encodeParam(a)
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i+1, err)
		}
		if t == TypeNull {
			b[nullAt+i/8] |= 1 << (i % 8)
		}
		b[types+2*i] = t
		if unsigned {
			b[types+2*i+1] = 0x80
		}
		b = append(b, val...)
	}
	return b, nil
}

// Execute sends COM_STMT_EXECUTE; sent is called just before the first
// byte goes out, so callers can tell "never sent" from "outcome unknown".
func (s *Stmt) Execute(ctx context.Context, args []any, sent func()) (*Rows, error) {
	pkt, err := s.ExecutePacket(args)
	if err != nil {
		return nil, err
	}
	c := s.c
	var cols []Column
	var ok *Result
	err = c.run(ctx, func() error {
		c.ResetSeq()
		if sent != nil {
			sent()
		}
		if err := c.WritePacket(pkt); err != nil {
			return err
		}
		var err error
		cols, ok, err = c.readResultHeader()
		return err
	})
	if err != nil {
		return nil, err
	}
	return c.startRows(ctx, true, cols, ok), nil
}

// Close sends COM_STMT_CLOSE, which has no response.
func (s *Stmt) Close(ctx context.Context) error {
	c := s.c
	return c.run(ctx, func() error {
		c.ResetSeq()
		return c.WritePacket(binary.LittleEndian.AppendUint32([]byte{ComStmtClose}, s.ID))
	})
}

// Ping sends COM_PING.
func (c *Conn) Ping(ctx context.Context) error {
	return c.run(ctx, func() error {
		c.ResetSeq()
		if err := c.WritePacket([]byte{ComPing}); err != nil {
			return err
		}
		_, ok, err := c.readResultHeader()
		if err == nil && ok == nil {
			c.broken = true
			return protoErr("COM_PING answered with a result set")
		}
		return err
	})
}
