package webauthn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// A minimal CBOR (RFC 8949) decoder for what WebAuthn sends: attestation
// objects and COSE keys. Maps decode to map[any]any (keys int64 or
// string), byte strings to []byte, text to string, arrays to []any,
// integers to int64. Indefinite lengths, tags and floats are refused: no
// authenticator response needs them. Depth and lengths are bounded.

var errCBOR = errors.New("webauthn: malformed CBOR")

const maxDepth = 16

type decoder struct {
	b   []byte
	off int
}

// decodeCBOR decodes one item and returns the bytes after it.
func decodeCBOR(b []byte) (any, []byte, error) {
	d := &decoder{b: b}
	v, err := d.item(0)
	if err != nil {
		return nil, nil, err
	}
	return v, b[d.off:], nil
}

func (d *decoder) head() (major byte, arg uint64, err error) {
	if d.off >= len(d.b) {
		return 0, 0, errCBOR
	}
	ib := d.b[d.off]
	d.off++
	major, info := ib>>5, ib&0x1f
	switch {
	case info < 24:
		return major, uint64(info), nil
	case info == 24:
		if d.off+1 > len(d.b) {
			return 0, 0, errCBOR
		}
		arg = uint64(d.b[d.off])
		d.off++
	case info == 25:
		if d.off+2 > len(d.b) {
			return 0, 0, errCBOR
		}
		arg = uint64(binary.BigEndian.Uint16(d.b[d.off:]))
		d.off += 2
	case info == 26:
		if d.off+4 > len(d.b) {
			return 0, 0, errCBOR
		}
		arg = uint64(binary.BigEndian.Uint32(d.b[d.off:]))
		d.off += 4
	case info == 27:
		if d.off+8 > len(d.b) {
			return 0, 0, errCBOR
		}
		arg = binary.BigEndian.Uint64(d.b[d.off:])
		d.off += 8
	default:
		return 0, 0, fmt.Errorf("%w: indefinite or reserved length", errCBOR)
	}
	return major, arg, nil
}

func (d *decoder) bytes(n uint64) ([]byte, error) {
	if n > uint64(len(d.b)-d.off) { //nolint:gosec // d.off never passes len(d.b)
		return nil, errCBOR
	}
	m := int(n) //nolint:gosec // bounded by the input's length just above
	out := d.b[d.off : d.off+m]
	d.off += m
	return out, nil
}

func (d *decoder) item(depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: too deep", errCBOR)
	}
	major, arg, err := d.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		if arg > math.MaxInt64 {
			return nil, errCBOR
		}
		return int64(arg), nil
	case 1:
		if arg > math.MaxInt64 {
			return nil, errCBOR
		}
		return -1 - int64(arg), nil
	case 2:
		b, err := d.bytes(arg)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case 3:
		b, err := d.bytes(arg)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case 4:
		if arg > uint64(len(d.b)) { // each element takes at least a byte
			return nil, errCBOR
		}
		out := make([]any, 0, arg)
		for range arg {
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 5:
		if arg > uint64(len(d.b)) {
			return nil, errCBOR
		}
		out := make(map[any]any, arg)
		for range arg {
			k, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, fmt.Errorf("%w: map key of type %T", errCBOR, k)
			}
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("%w: duplicate map key", errCBOR)
			}
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case 7:
		switch arg {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22:
			return nil, nil
		}
	}
	return nil, fmt.Errorf("%w: unsupported item (major %d)", errCBOR, major)
}
