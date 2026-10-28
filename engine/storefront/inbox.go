package storefront

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Keys derived from the public host's secret (TASKIEM_PUBLIC_KEY, 32
// bytes): one signs form tokens (public role only); the other encrypts
// submissions in the inbox, written by the public role and read by the
// api role. Neither can be used for the other's job.
func FormKey(master []byte) ([]byte, error)  { return derive(master, "taskiem storefront forms") }
func InboxKey(master []byte) ([]byte, error) { return derive(master, "taskiem storefront inbox") }

func derive(master []byte, info string) ([]byte, error) {
	if len(master) != 32 {
		return nil, errors.New("storefront: the public host's key must be 32 bytes")
	}
	return hkdf.Key(sha256.New, master, nil, info, 32)
}

// Submission is what the public host checked and the api role records:
// the customer's details stay inside it until they are sealed.
type Submission struct {
	Kind       string   `json:"kind"`
	Customer   Customer `json:"customer"`
	Basket     []Line   `json:"basket,omitempty"`
	TotalKobo  int64    `json:"total_kobo"`
	Fulfilment string   `json:"fulfilment,omitempty"` // pickup or delivery
	// Zone is the delivery area chosen and DeliveryKobo its fee, part of
	// TotalKobo (dispatch D1).
	Zone         string `json:"zone,omitempty"`
	DeliveryKobo int64  `json:"delivery_kobo,omitempty"`
	Payment    string   `json:"payment"`              // after_accept, at_order or none
	Channel    string   `json:"channel"`              // direct or embed
	// StatusKey is the secret of the customer's status link, so a payment
	// provider can send the customer back to it; it is not stored.
	StatusKey string    `json:"status_key"`
	Submitted time.Time `json:"submitted_at"`
	// Booking is a booking's service and time, held when it was sent.
	Booking *Booked `json:"booking,omitempty"`
	// DepositKobo is what a booking pays online when less than the total.
	DepositKobo int64 `json:"deposit_kobo,omitempty"`
}

// Booked is the slot a booking holds: Until is the end plus the service's
// buffer, Capacity the customers it takes at once.
type Booked struct {
	ServiceID   string    `json:"service_id"`
	ServiceName string    `json:"service_name"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Until       time.Time `json:"until"`
	Capacity    int       `json:"capacity"`
}

// Customer is who submitted. Phone is E.164.
type Customer struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Email    string `json:"email,omitempty"`
	Address  string `json:"address,omitempty"`
	Message  string `json:"message,omitempty"`
	BestTime string `json:"best_time,omitempty"`
}

// Line is one basket line, priced from the catalogue copy when submitted.
type Line struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	PriceKobo int64  `json:"price_kobo"`
	LineKobo  int64  `json:"line_kobo"`
}

// SealSubmission encrypts a submission for the inbox, bound to its
// storefront and reference.
func SealSubmission(key []byte, storefront uuid.UUID, reference string, s Submission) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, raw, []byte(storefront.String()+"\x00"+reference)), nil
}

// OpenSubmission decrypts what SealSubmission wrote.
func OpenSubmission(key []byte, storefront uuid.UUID, reference string, ct []byte) (Submission, error) {
	var s Submission
	g, err := gcm(key)
	if err != nil {
		return s, err
	}
	if len(ct) < g.NonceSize() {
		return s, errors.New("storefront: a short inbox payload")
	}
	raw, err := g.Open(nil, ct[:g.NonceSize()], ct[g.NonceSize():], []byte(storefront.String()+"\x00"+reference))
	if err != nil {
		return s, fmt.Errorf("storefront: an inbox payload that does not open: %w", err)
	}
	return s, json.Unmarshal(raw, &s)
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// NewReference is an order's reference: two letters from the storefront's
// slug and six characters customers can read out over the phone (no 0, O,
// 1, I): "AK-7QX4KD".
func NewReference(slug string) string {
	prefix := strings.ToUpper(strings.ReplaceAll(slug, "-", ""))
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	if len(prefix) < 2 {
		prefix = "TK"
	}
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	code := base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding).EncodeToString(b)
	return prefix + "-" + code[:6]
}

// NewStatusKey is the secret part of a customer's status link; the
// database keeps its hash.
func NewStatusKey() (key string, hash []byte) {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	key = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding).EncodeToString(b)
	return key, StatusHash(key)
}

// StatusHash is a status key's hash.
func StatusHash(key string) []byte {
	h := sha256.Sum256([]byte("storefront status\x00" + key))
	return h[:]
}
