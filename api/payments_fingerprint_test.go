package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// phoneKey is a phone's fingerprint key as the secure hardware makes it:
// ECDSA P-256, its public key DER SubjectPublicKeyInfo in base64.
type phoneKey struct {
	priv   *ecdsa.PrivateKey
	device string
}

func newPhoneKey(t *testing.T) *phoneKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &phoneKey{priv: k, device: uuid.NewString()}
}

func (k *phoneKey) public(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func (k *phoneKey) sign(t *testing.T, words string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(words))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func (k *phoneKey) body(t *testing.T, proof map[string]any) map[string]any {
	t.Helper()
	b := map[string]any{"public_key": k.public(t), "device": map[string]any{"id": k.device, "name": "Ada's Pixel", "platform": "android"}}
	for n, v := range proof {
		b[n] = v
	}
	return b
}

// fpPin is a PIN of the length the PIN has now.
var fpPin = "48291375"[:whatsapp.PinLength]

// Turning the fingerprint on: a passkey, the PIN or a code to the
// person's phone proves it, nothing else does; one key a phone, replaced
// when turned on again; listed and removed, each audited.
func TestFingerprintTurnOn(t *testing.T) {
	ctx := context.Background()
	w, owner := payPinWorld(t)
	me := userID(t, owner)
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_wa_pin_set($1, $2)`, me, pinHash(fpPin)); err != nil {
		t.Fatal(err)
	}
	sms := &fakeSMS{}
	w.srv.CodeSMS = sms
	const phone = "+2348010000077"
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_auth_phone_claim($1, $2)`, me, phone); err != nil {
		t.Fatal(err)
	}
	got := owner.must(200, "GET", "/v1/me/fingerprint", nil)
	if len(got["devices"].([]any)) != 0 || got["one_kobo"] != float64(50000000) || got["day_kobo"] != float64(100000000) ||
		stringOf(got["proofs"]) != stringOf([]any{"pin", "code"}) {
		t.Fatalf("before: %v", got)
	}

	k := newPhoneKey(t)
	// No proof: the ways to prove.
	if e := owner.must(403, "POST", "/v1/me/fingerprint", k.body(t, nil)); e["code"] != "proof_needed" {
		t.Fatalf("no proof: %v", e)
	}
	// Not a P-256 key.
	bad := k.body(t, map[string]any{"pin": fpPin})
	bad["public_key"] = base64.StdEncoding.EncodeToString([]byte("not a key at all, not a key at all, not a key at all, not a key at all"))
	owner.must(400, "POST", "/v1/me/fingerprint", bad)
	// A wrong PIN is counted with the PIN's tries.
	if e := owner.must(403, "POST", "/v1/me/fingerprint", k.body(t, map[string]any{"pin": strings.Repeat("0", whatsapp.PinLength-1) + "7"})); !strings.Contains(fmt.Sprint(e["error"]), "4 tries left") {
		t.Fatalf("a wrong PIN: %v", e)
	}
	// The PIN proves it.
	d := owner.must(201, "POST", "/v1/me/fingerprint", k.body(t, map[string]any{"pin": fpPin}))["device"].(map[string]any)
	if d["name"] != "Ada's Pixel" || d["platform"] != "android" || d["device_id"] != k.device {
		t.Fatalf("turned on: %v", d)
	}
	// A code to the phone proves it too; on the same phone it replaces the key.
	owner.must(202, "POST", "/v1/me/fingerprint/code", map[string]any{"via": "sms"})
	if e := owner.must(401, "POST", "/v1/me/fingerprint", k.body(t, map[string]any{"code": "000000"})); e["code_wrong"] != true {
		t.Fatalf("a wrong code: %v", e)
	}
	owner.must(202, "POST", "/v1/me/fingerprint/code", map[string]any{"via": "sms"})
	k2 := newPhoneKey(t)
	k2.device = k.device
	d2 := owner.must(201, "POST", "/v1/me/fingerprint", k2.body(t, map[string]any{"code": sms.code(t, phone)}))["device"].(map[string]any)
	if d2["id"] == d["id"] {
		t.Fatalf("not replaced: %v %v", d, d2)
	}
	// A passkey proves it, on another phone.
	key := addPasskey(t, owner, "Phone")
	k3 := newPhoneKey(t)
	owner.must(201, "POST", "/v1/me/fingerprint", k3.body(t, map[string]any{"passkey": stepUp(t, owner, key, "account.reauth", "fingerprint.add")}))
	got = owner.must(200, "GET", "/v1/me/fingerprint", nil)
	if n := len(got["devices"].([]any)); n != 2 || stringOf(got["proofs"]) != stringOf([]any{"passkey", "pin", "code"}) {
		t.Fatalf("two phones: %v", got)
	}

	// Someone else cannot remove it; the person can, with no proof.
	other := w.tenant(t, "Bola Foods", "bola@fingerprint.test")
	other.must(404, "DELETE", "/v1/me/fingerprint/"+d2["id"].(string), nil)
	owner.must(204, "DELETE", "/v1/me/fingerprint/"+d2["id"].(string), nil)
	owner.must(404, "DELETE", "/v1/me/fingerprint/"+d2["id"].(string), nil)
	if n := len(owner.must(200, "GET", "/v1/me/fingerprint", nil)["devices"].([]any)); n != 1 {
		t.Fatalf("removed: %d", n)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"fingerprint.add"`, `"fingerprint.remove"`, `"fingerprint.pin.fail"`, `"via":"code"`, `"via":"pin"`, `"via":"passkey"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
}

// Paying with the fingerprint: a challenge bound to one payment and one
// key, signed by the phone, pays within its own limits (₦500,000 a
// payment, ₦1,000,000 a day) with step-up "fingerprint"; a challenge
// works once; a signature by another key, a changed account or a row of
// a list does not pay; a removed key cannot.
func TestPayByFingerprint(t *testing.T) {
	ctx := context.Background()
	w, owner := payPinWorld(t)
	key := addPasskey(t, owner, "Phone")
	k := newPhoneKey(t)
	keyID := owner.must(201, "POST", "/v1/me/fingerprint", k.body(t, map[string]any{"passkey": stepUp(t, owner, key, "account.reauth", "fingerprint.add")}))["device"].(map[string]any)["id"].(string)
	prep := func(naira int) string {
		t.Helper()
		return owner.must(201, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": naira * 100, "purpose": "delivery"})["payment"].(map[string]any)["id"].(string)
	}
	challenge := func(id string) (int, map[string]any) {
		t.Helper()
		return owner.do("POST", "/v1/payments/"+id+"/fingerprint", map[string]any{"key_id": keyID})
	}
	pay := func(id, words, sig string, more map[string]any) (int, map[string]any) {
		t.Helper()
		b := map[string]any{"key_id": keyID, "challenge": words, "signature": sig}
		for n, v := range more {
			b[n] = v
		}
		return owner.do("POST", "/v1/payments/"+id+"/fingerprint/confirm", b)
	}

	// Within its limits: the challenge names the payment; signed, it pays.
	id := prep(300000)
	st, c := challenge(id)
	words, _ := c["challenge"].(string)
	if st != 200 || !strings.Contains(words, id) || !strings.Contains(words, "30000000") || c["limit_kobo"] != float64(50000000) {
		t.Fatalf("challenge: %d %v", st, c)
	}
	// Signed by another key: refused, audited, and the challenge is spent.
	if st, e := pay(id, words, newPhoneKey(t).sign(t, words), nil); st != 403 || e["code"] != "fingerprint_wrong" {
		t.Fatalf("another key: %d %v", st, e)
	}
	if st, e := pay(id, words, k.sign(t, words), nil); st != 403 || e["code"] != "fingerprint_expired" {
		t.Fatalf("a spent challenge: %d %v", st, e)
	}
	_, c = challenge(id)
	words = c["challenge"].(string)
	st, e := pay(id, words, k.sign(t, words), map[string]any{"tell_payee": false})
	if st != 202 || e["payment"].(map[string]any)["status"] != "sending" {
		t.Fatalf("paid: %d %v", st, e)
	}
	var with, su string
	var tell bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.confirmed_with, d.step_up, p.tell_payee FROM payments p JOIN approval_decisions d ON d.run_id = p.run_id WHERE p.id = $1`, id).
		Scan(&with, &su, &tell); err != nil || with != "fingerprint" || su != "fingerprint" || tell {
		t.Fatalf("confirmed with: %q %q %v %v", with, su, tell, err)
	}
	// Used once: the same words again do not pay another.
	other := prep(1000)
	if st, e := pay(other, words, k.sign(t, words), nil); st != 403 || e["code"] != "fingerprint_expired" {
		t.Fatalf("another payment's words: %d %v", st, e)
	}

	// Over ₦500,000 a payment: no challenge.
	if st, e := challenge(prep(600000)); st != 403 || e["code"] != "fingerprint_limit" || !strings.Contains(fmt.Sprint(e["error"]), "₦500,000 a payment") {
		t.Fatalf("over one: %d %v", st, e)
	}
	// The day's ₦1,000,000: ₦300,000 + ₦500,000 fit, ₦250,000 more does not.
	next := prep(500000)
	_, c = challenge(next)
	if st, e := pay(next, c["challenge"].(string), k.sign(t, c["challenge"].(string)), nil); st != 202 {
		t.Fatalf("the second: %d %v", st, e)
	}
	if st, e := challenge(prep(250000)); st != 403 || e["code"] != "fingerprint_limit" || !strings.Contains(fmt.Sprint(e["error"]), "₦200,000 left today") {
		t.Fatalf("over the day: %d %v", st, e)
	}
	if got := owner.must(200, "GET", "/v1/me/fingerprint", nil); got["left_today_kobo"] != float64(20000000) {
		t.Errorf("left today: %v", got)
	}
	// Its day is its own: the PIN's is untouched.
	var pinToday int64
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_pin_paid_today($1)`, userID(t, owner)).Scan(&pinToday); err != nil || pinToday != 0 {
		t.Errorf("the PIN's day: %d %v", pinToday, err)
	}

	// A changed account: the passkey only.
	var bayo string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT saved_id FROM payments WHERE id = $1`, id).Scan(&bayo); err != nil {
		t.Fatal(err)
	}
	owner.must(200, "PUT", "/v1/saved/"+bayo, map[string]any{"account_number": "0987654321",
		"passkey": key.Assert(challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": bayo}))).JSON()})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET confirmed_at = now() - interval '2 days' WHERE confirmed_with = 'fingerprint'`); err != nil {
		t.Fatal(err)
	}
	if st, e := challenge(prep(20000)); st != 403 || e["code"] != "fingerprint_passkey_only" {
		t.Fatalf("a changed account: %d %v", st, e)
	}
	// A row of a list: the passkey only.
	b := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{{"payee": "bayo", "amount_kobo": 2000000, "purpose": "delivery"}}})["batch"].(map[string]any)
	row := b["payments"].([]any)[0].(map[string]any)["id"].(string)
	if st, e := challenge(row); st != 403 || e["code"] != "fingerprint_passkey_only" {
		t.Fatalf("a row of a list: %d %v", st, e)
	}

	// Paid once by passkey, the account is known again; then removed: no
	// challenge, and an earlier one does not pay.
	known := prep(10000)
	owner.must(202, "POST", "/v1/payments/"+known+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+known)})
	last := prep(12000)
	st, c = challenge(last)
	if st != 200 {
		t.Fatalf("after paying by passkey: %d %v", st, c)
	}
	owner.must(204, "DELETE", "/v1/me/fingerprint/"+keyID, nil)
	if st, e := pay(last, c["challenge"].(string), k.sign(t, c["challenge"].(string)), nil); st != 403 || e["code"] != "fingerprint_expired" {
		t.Fatalf("a removed key's words: %d %v", st, e)
	}
	if st, e := challenge(last); st != 403 || e["code"] != "no_fingerprint" {
		t.Fatalf("a removed key: %d %v", st, e)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"payment.fingerprint.fail"`, `"step_up":"fingerprint"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
}
