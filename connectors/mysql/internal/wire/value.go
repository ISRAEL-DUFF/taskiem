package wire

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

// Decimal is a DECIMAL value exactly as the server sent it.
type Decimal string

// DateTime is a DATE, DATETIME or TIMESTAMP value. It carries no time
// zone: TIMESTAMPs are rendered in the session time zone.
type DateTime struct {
	Year                 uint16
	Month, Day           uint8
	Hour, Minute, Second uint8
	Microsecond          uint32
}

// Time converts to a UTC time.Time; false for zero or invalid dates
// (such as 0000-00-00, which MySQL may allow).
func (d DateTime) Time() (time.Time, bool) {
	if d.Month < 1 || d.Month > 12 || d.Day < 1 || d.Day > 31 {
		return time.Time{}, false
	}
	t := time.Date(int(d.Year), time.Month(d.Month), int(d.Day), int(d.Hour), int(d.Minute), int(d.Second), int(d.Microsecond)*1000, time.UTC)
	if t.Day() != int(d.Day) {
		return time.Time{}, false
	}
	return t, true
}

// DateString formats the date part as YYYY-MM-DD.
func (d DateTime) DateString() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// String formats as MySQL does: YYYY-MM-DD HH:MM:SS[.ffffff].
func (d DateTime) String() string {
	s := fmt.Sprintf("%s %02d:%02d:%02d", d.DateString(), d.Hour, d.Minute, d.Second)
	if d.Microsecond != 0 {
		s += fmt.Sprintf(".%06d", d.Microsecond)
	}
	return s
}

// Duration is a TIME value, which may be negative and exceed 24 hours.
type Duration struct {
	Negative             bool
	Days                 uint32
	Hour, Minute, Second uint8
	Microsecond          uint32
}

// String formats as MySQL does: [-]HHH:MM:SS[.ffffff].
func (d Duration) String() string {
	var b strings.Builder
	if d.Negative {
		b.WriteByte('-')
	}
	fmt.Fprintf(&b, "%02d:%02d:%02d", uint64(d.Days)*24+uint64(d.Hour), d.Minute, d.Second)
	if d.Microsecond != 0 {
		fmt.Fprintf(&b, ".%06d", d.Microsecond)
	}
	return b.String()
}

// decodeBinaryRow decodes a Binary Protocol Resultset Row. Values are nil,
// int64, uint64, float32, float64, Decimal, DateTime, Duration or []byte.
func decodeBinaryRow(p []byte, cols []Column) ([]any, error) {
	if len(p) == 0 || p[0] != 0x00 {
		return nil, protoErr("binary row does not start with 0x00")
	}
	r := reader{b: p[1:]}
	nulls := r.take((len(cols)+7+2)/8, "NULL bitmap")
	row := make([]any, len(cols))
	for i, col := range cols {
		if r.err != nil {
			break
		}
		if nulls[(i+2)/8]&(1<<((i+2)%8)) != 0 {
			continue
		}
		v, err := decodeBinaryValue(&r, col)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", col.Name, err)
		}
		row[i] = v
	}
	if err := r.done("binary row"); err != nil {
		return nil, err
	}
	return row, nil
}

func decodeBinaryValue(r *reader, col Column) (any, error) {
	u := col.Unsigned()
	switch col.Type {
	case TypeTiny:
		v := r.u8("TINY")
		if u {
			return uint64(v), r.err
		}
		return int64(int8(v)), r.err // #nosec G115 -- two's complement reinterpretation
	case TypeShort, TypeYear:
		v := r.u16("SHORT")
		if u || col.Type == TypeYear {
			return uint64(v), r.err
		}
		return int64(int16(v)), r.err // #nosec G115 -- two's complement reinterpretation
	case TypeInt24, TypeLong:
		v := r.u32("LONG")
		if u {
			return uint64(v), r.err
		}
		return int64(int32(v)), r.err // #nosec G115 -- two's complement reinterpretation
	case TypeLongLong:
		v := r.u64("LONGLONG")
		if u {
			return v, r.err
		}
		return int64(v), r.err // #nosec G115 -- two's complement reinterpretation
	case TypeFloat:
		return math.Float32frombits(r.u32("FLOAT")), r.err
	case TypeDouble:
		return math.Float64frombits(r.u64("DOUBLE")), r.err
	case TypeNull:
		return nil, nil
	case TypeDate, TypeNewDate, TypeDateTime, TypeTimestamp, TypeDateTime2, TypeTimestamp2:
		return decodeBinaryDateTime(r)
	case TypeTime, TypeTime2:
		return decodeBinaryTime(r)
	case TypeDecimal, TypeNewDecimal:
		b, _ := r.lenencBytes("DECIMAL")
		return Decimal(b), r.err
	case TypeVarchar, TypeBit, TypeJSON, TypeEnum, TypeSet, TypeTinyBlob, TypeMediumBlob,
		TypeLongBlob, TypeBlob, TypeVarString, TypeString, TypeGeometry, TypeVector:
		b, _ := r.lenencBytes("string")
		if r.err != nil {
			return nil, r.err
		}
		return append([]byte(nil), b...), nil
	}
	return nil, protoErr("unknown column type %d", col.Type)
}

func decodeBinaryDateTime(r *reader) (any, error) {
	var d DateTime
	switch n := r.u8("DATETIME length"); n {
	case 0:
	case 4, 7, 11:
		d.Year = r.u16("year")
		d.Month, d.Day = r.u8("month"), r.u8("day")
		if n >= 7 {
			d.Hour, d.Minute, d.Second = r.u8("hour"), r.u8("minute"), r.u8("second")
		}
		if n == 11 {
			d.Microsecond = r.u32("microsecond")
		}
	default:
		if r.err == nil {
			return nil, protoErr("DATETIME length %d", n)
		}
	}
	return d, r.err
}

func decodeBinaryTime(r *reader) (any, error) {
	var d Duration
	switch n := r.u8("TIME length"); n {
	case 0:
	case 8, 12:
		d.Negative = r.u8("is_negative") == 1
		d.Days = r.u32("days")
		d.Hour, d.Minute, d.Second = r.u8("hour"), r.u8("minute"), r.u8("second")
		if n == 12 {
			d.Microsecond = r.u32("microsecond")
		}
	default:
		if r.err == nil {
			return nil, protoErr("TIME length %d", n)
		}
	}
	return d, r.err
}

// decodeTextRow decodes a text-protocol row: each value is a
// length-encoded string ([]byte) or NULL (0xFB → nil).
func decodeTextRow(p []byte, n int) ([]any, error) {
	r := reader{b: p}
	row := make([]any, n)
	for i := range row {
		b, null := r.lenencBytes("text value")
		if !null && r.err == nil {
			row[i] = append([]byte(nil), b...)
		}
	}
	if err := r.done("text row"); err != nil {
		return nil, err
	}
	return row, nil
}

// encodeParam encodes one statement parameter in the binary protocol.
func encodeParam(a any) (typ byte, unsigned bool, val []byte, err error) {
	le := binary.LittleEndian
	switch v := a.(type) {
	case nil:
		return TypeNull, false, nil, nil
	case bool:
		if v {
			return TypeTiny, false, []byte{1}, nil
		}
		return TypeTiny, false, []byte{0}, nil
	case int:
		return TypeLongLong, false, le.AppendUint64(nil, uint64(v)), nil // #nosec G115 -- two's complement on the wire
	case int8:
		return TypeLongLong, false, le.AppendUint64(nil, uint64(v)), nil // #nosec G115 -- two's complement on the wire
	case int16:
		return TypeLongLong, false, le.AppendUint64(nil, uint64(v)), nil // #nosec G115 -- two's complement on the wire
	case int32:
		return TypeLongLong, false, le.AppendUint64(nil, uint64(v)), nil // #nosec G115 -- two's complement on the wire
	case int64:
		return TypeLongLong, false, le.AppendUint64(nil, uint64(v)), nil // #nosec G115 -- two's complement on the wire
	case uint:
		return TypeLongLong, true, le.AppendUint64(nil, uint64(v)), nil
	case uint32:
		return TypeLongLong, true, le.AppendUint64(nil, uint64(v)), nil
	case uint64:
		return TypeLongLong, true, le.AppendUint64(nil, v), nil
	case float32:
		return TypeDouble, false, le.AppendUint64(nil, math.Float64bits(float64(v))), nil
	case float64:
		return TypeDouble, false, le.AppendUint64(nil, math.Float64bits(v)), nil
	case string:
		return TypeVarString, false, AppendLenencBytes(nil, []byte(v)), nil
	case []byte:
		return TypeBlob, false, AppendLenencBytes(nil, v), nil
	case Decimal:
		return TypeNewDecimal, false, AppendLenencBytes(nil, []byte(v)), nil
	case time.Time:
		v = v.UTC()
		if v.Year() < 0 || v.Year() > 9999 {
			return 0, false, nil, fmt.Errorf("%w: time %v is outside MySQL's range", ErrConfig, v)
		}
		b := []byte{11}
		b = le.AppendUint16(b, uint16(v.Year()))                                                          // #nosec G115 -- range checked above
		b = append(b, byte(v.Month()), byte(v.Day()), byte(v.Hour()), byte(v.Minute()), byte(v.Second())) // #nosec G115 -- calendar fields fit a byte
		b = le.AppendUint32(b, uint32(v.Nanosecond()/1000))                                               // #nosec G115 -- below 1e6
		return TypeDateTime, false, b, nil
	}
	return 0, false, nil, fmt.Errorf("%w: unsupported parameter type %T", ErrConfig, a)
}
