package dispatch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The delivery proved by a form step (docs/spec/device-fields.md §6, D1):
// the rider's answer to "Delivered" (a location and a photo, each with the
// source the server set) is recorded on the business's delivery, so the
// Confirm card shows the pin at its masked point and the photo, and says
// which was read by the phone and which was sent.

// Proof is a form step's answer as riders@1 record_proof takes it.
type Proof struct {
	// Lat and Lng are the location answered, nil when none was; Source is
	// how it was got (device, sent or typed), Coarse past max_accuracy_m.
	Lat, Lng    *float64
	WhereSource string
	Coarse      bool
	AccuracyM   *float64
	// Photo is the form file answered (its id), and PhotoSource camera or sent.
	Photo       string
	PhotoSource string
	// Run is the run recording the proof: the photo must be a file
	// answered to it (a form step of the run, or the form that started
	// it), never another run's file named by its id.
	Run uuid.UUID
}

// Proof marks: what the location says of where the parcel was handed over.
const (
	MarkAtDoor      = "confirmed_at_door"
	MarkFar         = "far_from_address"
	MarkSaidByRider = "said_by_rider"
	MarkReadByPhone = "read_by_phone"
	MarkNoLocation  = "no_location"
)

// DoorRadiusM is how near the customer's own pin a reading must be to
// confirm the delivery at the door.
const DoorRadiusM = 150

// MaskPoint rounds a coordinate to three decimal places, about 110 m, as
// a location is masked in every card (device fields §5).
func MaskPoint(v float64) float64 { return math.Round(v*1000) / 1000 }

var pinRe = regexp.MustCompile(`(-?\d{1,2}\.\d+)\s*,\s*(-?\d{1,3}\.\d+)`)

// pinOf reads a maps link's point.
func pinOf(link string) (float64, float64, bool) {
	m := pinRe.FindStringSubmatch(link)
	if m == nil {
		return 0, 0, false
	}
	lat, e1 := strconv.ParseFloat(m[1], 64)
	lng, e2 := strconv.ParseFloat(m[2], 64)
	return lat, lng, e1 == nil && e2 == nil
}

// DistanceM is the great-circle distance between two points in metres.
func DistanceM(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// MarkOf is the mark a proof earns against the customer's pin (a maps
// link; "" when they gave none): a pin sent in a chat is the rider's word.
func MarkOf(p Proof, customerPin string) string {
	switch {
	case p.Lat == nil || p.Lng == nil:
		return MarkNoLocation
	case p.WhereSource != "device":
		return MarkSaidByRider
	}
	lat, lng, ok := pinOf(customerPin)
	if !ok {
		return MarkReadByPhone
	}
	if DistanceM(*p.Lat, *p.Lng, lat, lng) <= DoorRadiusM && !p.Coarse {
		return MarkAtDoor
	}
	return MarkFar
}

// RecordProof records a form step's proof on the delivery of the order
// reference (held or delivered): the location as its masked point and
// mark, the photo (a form file of the business's) beside the delivery's
// other photos with its source. It answers the mark.
func (d *Dispatch) RecordProof(ctx context.Context, tenant uuid.UUID, reference string, p Proof) (string, error) {
	var mark string
	err := d.tx(ctx, tenant, func(tx pgx.Tx) error {
		ord, err := d.Orders.GetOrder(ctx, tx, tenant, reference)
		if err != nil {
			return err
		}
		x, err := d.HeldTx(ctx, tx, tenant, ord.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s has no rider", ErrNoDelivery, reference)
		}
		if err != nil {
			return err
		}
		if x.Status == "offered" {
			return fmt.Errorf("%w: %s is not with a rider yet", ErrMove, reference)
		}
		mark = MarkOf(p, ord.Customer.Pin)
		var where map[string]any
		if p.Lat != nil && p.Lng != nil {
			where = map[string]any{"lat": MaskPoint(*p.Lat), "lng": MaskPoint(*p.Lng), "source": p.WhereSource}
			if p.Coarse {
				where["coarse"] = true
			}
			if p.AccuracyM != nil {
				where["accuracy_m"] = math.Round(*p.AccuracyM)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET proof_where = $2, proof_mark = $3 WHERE id = $1`, x.ID, where, mark); err != nil {
			return err
		}
		if p.Photo == "" {
			return nil
		}
		fid, err := uuid.Parse(p.Photo)
		if err != nil {
			return fmt.Errorf("%w: the photo is a form file's id", ErrInvalid)
		}
		var path string
		// Answered to this run: a file of a submission to one of its form
		// steps' requests, or of the form that started it.
		if err := tx.QueryRow(ctx, `SELECT f.storage_key FROM form_files f JOIN form_submissions s ON s.id = f.submission_id
			LEFT JOIN form_requests q ON q.id = s.request_id
			WHERE f.id = $1 AND s.status <> 'refused' AND (q.run_id = $2 OR s.run_id = $2)`, fid, p.Run).Scan(&path); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: no such photo answered to this run", ErrInvalid)
			}
			return err
		}
		src := p.PhotoSource
		if src != "camera" {
			src = "sent"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_proofs (id, tenant_id, delivery_id, path, source) VALUES ($1, $2, $3, $4, $5)`,
			uuid.Must(uuid.NewV7()), tenant, x.ID, path, src); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE deliveries SET proof = $2 WHERE id = $1`, x.ID, path)
		return err
	})
	return mark, err
}

// ProofView is what a delivery's Confirm card shows of its proof.
type ProofView struct {
	// Where is the masked point and how it was got; Mark what it says.
	Where *ProofWhere `json:"where,omitempty"`
	Mark  string      `json:"mark,omitempty"`
	// Photo is the latest photo's place in storage, PhotoSource camera or
	// sent (empty for a photo from before D1, sent in a chat).
	Photo       string `json:"-"`
	PhotoSource string `json:"photo_source,omitempty"`
}

// ProofWhere is a delivery's location, masked to about 100 m.
type ProofWhere struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	Source    string   `json:"source"`
	Coarse    bool     `json:"coarse,omitempty"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
}

// ProofsTx are deliveries' proofs, by delivery, in the business's scope.
func ProofsTx(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) (map[uuid.UUID]ProofView, error) {
	out := map[uuid.UUID]ProofView{}
	rows, err := tx.Query(ctx, `SELECT d.id, d.proof_where, COALESCE(d.proof_mark, ''), COALESCE(d.proof, ''),
		COALESCE((SELECT p.source FROM delivery_proofs p WHERE p.delivery_id = d.id ORDER BY p.created_at DESC, p.id DESC LIMIT 1), '')
		FROM deliveries d WHERE d.id = ANY ($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var where *ProofWhere
		var v ProofView
		if err := rows.Scan(&id, &where, &v.Mark, &v.Photo, &v.PhotoSource); err != nil {
			return nil, err
		}
		v.Where = where
		out[id] = v
	}
	return out, rows.Err()
}
