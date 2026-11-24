package pii

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SealFields seals some fields of the records at a place in v: segs names
// the place from v's root, ending at a record (an object) or at a list of
// records, and pick says which of a record's fields are personal. Each
// picked value is sealed as a value under a declared object is (sealUnder,
// in category cat): strings remembered in taint, numbers and booleans
// sealed but not followed. Every other field is left as it is. It is how a
// data table's rows are sealed column by column (docs/privacy.md).
//
// v is not modified. ok is false, with nothing sealed, when the place
// holds something other than records, or more picked values than
// MaxSealedLeaves: the caller then seals the place whole, as it would
// without the columns. A missing place is ok and seals nothing.
func SealFields(ctx context.Context, c Cipher, tx pgx.Tx, tenant uuid.UUID, v any, segs []string, pick func(field string) bool, cat string, taint Taint) (out any, ok bool, err error) {
	if c == nil {
		return v, true, nil
	}
	v = deepCopy(v)
	at := v
	for _, s := range segs {
		m, isMap := at.(map[string]any)
		if !isMap {
			return v, true, nil
		}
		if at, isMap = m[s]; !isMap {
			return v, true, nil
		}
	}
	var records []map[string]any
	switch t := at.(type) {
	case nil:
		return v, true, nil
	case map[string]any:
		if IsEnvelope(t) {
			return v, true, nil
		}
		records = []map[string]any{t}
	case []any:
		for _, x := range t {
			r, isMap := x.(map[string]any)
			if !isMap || IsEnvelope(r) {
				return v, false, nil
			}
			records = append(records, r)
		}
	default:
		return v, false, nil
	}
	n := 0
	for _, r := range records {
		for k, x := range r {
			if pick(k) {
				if n += leaves(x, MaxSealedLeaves+1-n); n > MaxSealedLeaves {
					return v, false, nil
				}
			}
		}
	}
	for _, r := range records {
		for k, x := range r {
			if !pick(k) {
				continue
			}
			s, err := sealUnder(ctx, c, tx, tenant, x, cat, nil, taint, 0)
			if err != nil {
				return nil, false, err
			}
			r[k] = s
		}
	}
	return v, true, nil
}
