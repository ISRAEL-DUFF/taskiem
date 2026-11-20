package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// Saved files after S1's review: a person's file sealed under their key
// (never its bytes in the row), served never cached; uploads waiting to be
// claimed capped; one never claimed purged by the sweep, a business's copy
// with it.
func TestSavedFilesKeptSafe(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	ps := newProjectStorage(t)
	w.srv.Media = ps.service()
	w.srv.Media.Scanner = chatScanner{}
	tolu, _ := w.personIn(t, "+2348031120071")
	toluID := personID(t, tolu)
	upload := func(c *client, path string, shade uint8) (int, map[string]any) {
		t.Helper()
		return c.postFile(t, path, "logo.png", pngPhoto(t, shade), nil)
	}
	code, out := upload(tolu, "/v1/me/saved/uploads", 40)
	if code != 201 {
		t.Fatalf("upload: %d %v", code, out)
	}
	up := out["upload"].(map[string]any)
	var data []byte
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT data FROM saved_files WHERE person_id = $1`, toluID).Scan(&data); err != nil || bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
		t.Fatalf("a person's file kept as it is: %v", err)
	}
	logo := tolu.must(201, "POST", "/v1/me/saved", map[string]any{"name": "logo", "title": "Logo", "kind": "file", "details": map[string]any{"file": up["file"]}})["saved"].(map[string]any)
	st, hdr, body := tolu.rawGet(t, "/v1/me/saved/"+logo["id"].(string)+"/file")
	if st != 200 || !strings.HasPrefix(body, "\xff\xd8\xff") || hdr.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("opened: %d %q %q", st, hdr.Get("Cache-Control"), body[:min(3, len(body))])
	}

	// 20 waiting at most.
	for i := 0; i < 20; i++ {
		if code, out := upload(tolu, "/v1/me/saved/uploads", uint8(50+i)); code != 201 {
			t.Fatalf("upload %d: %d %v", i, code, out)
		}
	}
	if code, out := upload(tolu, "/v1/me/saved/uploads", 99); code != 400 && code != 409 || !strings.Contains(fmt.Sprint(out["error"]), "waiting to be saved") {
		t.Fatalf("the 21st waiting: %d %v", code, out)
	}

	// Too many at once: wait.
	for i := 0; ; i++ {
		code, _ := upload(tolu, "/v1/me/saved/uploads", 99)
		if code == 429 {
			break
		}
		if i > 20 {
			t.Fatal("uploads never limited")
		}
	}

	// The sweep lets go of uploads a day on, the business's copy too.
	if code, out := upload(w.owner, "/v1/saved/uploads", 60); code != 201 {
		t.Fatalf("business upload: %d %v", code, out)
	}
	ps.mu.Lock()
	before := len(ps.files)
	ps.mu.Unlock()
	if before != 1 {
		t.Fatalf("stored: %d", before)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE saved_files SET created_at = now() - interval '2 days' WHERE object_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.WhatsAppTick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	var left int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_files WHERE object_id IS NULL`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("waiting after the sweep: %d %v", left, err)
	}
	ps.mu.Lock()
	after := len(ps.files)
	ps.mu.Unlock()
	if after != 0 {
		t.Fatalf("the business's copy left: %d", after)
	}
	// The kept one stays.
	if st, _, _ := tolu.rawGet(t, "/v1/me/saved/"+logo["id"].(string)+"/file"); st != 200 {
		t.Fatalf("the kept file: %d", st)
	}
}

// A person's places are their addresses wherever they are saved: the first
// saved on Saved is the default and counts toward 20; an address changed
// keeps the place's note; a removal lets go of what it held.
func TestSavedPlacesAreAddresses(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	ada, _ := w.personIn(t, "+2348031120072")
	home := ada.must(201, "POST", "/v1/me/saved", map[string]any{"name": "home", "title": "Home", "kind": "place",
		"details": map[string]any{"line": "4 Allen Avenue", "area": "Ikeja", "note": "the blue gate"}})["saved"].(map[string]any)
	list := ada.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["default"] != true {
		t.Fatalf("the first place: %v", list)
	}
	ada.must(200, "PUT", "/v1/me/addresses/"+home["id"].(string), map[string]any{"landmark": "opposite the bakery"})
	got := ada.must(200, "GET", "/v1/me/saved/"+home["id"].(string), nil)
	if d := got["details"].(map[string]any); d["note"] != "the blue gate" || d["landmark"] != "opposite the bakery" {
		t.Fatalf("after the address changed: %v", d)
	}
	for i := 2; i <= 20; i++ {
		ada.must(201, "POST", "/v1/me/addresses", map[string]any{"label": fmt.Sprintf("Place %d", i), "line": fmt.Sprintf("%d Allen Avenue", i)})
	}
	if e := ada.must(400, "POST", "/v1/me/saved", map[string]any{"name": "one-more", "title": "One more", "kind": "place", "details": map[string]any{"line": "21 Allen Avenue"}}); !strings.Contains(fmt.Sprint(e["error"]), "up to 20") {
		t.Fatalf("the 21st: %v", e)
	}
	ada.must(204, "DELETE", "/v1/me/saved/"+home["id"].(string), nil)
	var held string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT details::text || sealed::text || hint FROM saved_objects WHERE id = $1`, home["id"]).Scan(&held); err != nil || held != "{}{}" {
		t.Fatalf("a removed place holds: %q %v", held, err)
	}
	if list := ada.must(200, "GET", "/v1/me/addresses", nil)["addresses"].([]any); len(list) != 19 || list[0].(map[string]any)["default"] != true {
		t.Fatalf("a default after: %v", list)
	}
}

// A business's people are listed with their phone and email masked; an
// erasure by phone lets go of the business's saved things holding it.
func TestSavedContactsMaskedAndErased(t *testing.T) {
	ctx := context.Background()
	w, owner := savedWorld(t)
	bisi := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bisi", "title": "Bisi", "kind": "person", "details": map[string]any{"phone": "0803 112 0077"}})["saved"].(map[string]any)
	if bisi["hint"] != "••0077" {
		t.Fatalf("hint: %v", bisi["hint"])
	}
	em := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "tayo", "title": "Tayo", "kind": "person", "details": map[string]any{"email": "tayo@shop.ng"}})["saved"].(map[string]any)
	if em["hint"] != "t•••@shop.ng" {
		t.Fatalf("email hint: %v", em["hint"])
	}
	k := owner.must(201, "POST", "/v1/saved-kinds", map[string]any{"name": "Rider", "fields": []map[string]any{{"label": "Phone", "type": "phone", "sensitive": true}}})["kind"].(map[string]any)
	rider := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "rider", "title": "Rider", "kind": "custom", "kind_id": k["id"], "values": map[string]any{"phone": "08031120077"}})["saved"].(map[string]any)
	keep := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "other", "title": "Other", "kind": "person", "details": map[string]any{"phone": "08031120066"}})["saved"].(map[string]any)

	secret := enrol(t, owner)
	out := owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "phone", "value": "+234 803 112 0077", "totp": codeAfter(secret, 1)})
	if out["saved"] != float64(2) {
		t.Fatalf("erased: %v", out)
	}
	for _, id := range []any{bisi["id"], rider["id"]} {
		var held string
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT details::text || sealed::text FROM saved_objects WHERE id = $1 AND archived_at IS NOT NULL`, id).Scan(&held); err != nil || held != "{}{}" {
			t.Fatalf("erased thing holds: %q %v", held, err)
		}
	}
	owner.must(200, "GET", "/v1/saved/"+keep["id"].(string), nil)
}

// On a business's own WhatsApp app, the sender is only as sure as the
// secret the business chose: a person's own saved things are neither
// saved nor looked up there, and a save is not kept to oneself. A person's
// own save waiting for their yes is sealed under their key, not the
// business's.
func TestSavedChatOwnAppAndPersonalSeal(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.graph.AddNumber(pnA, tokA, "+234 800 000 0001")
	alpha := w.tenant(t, "Alpha", "owner@alpha.test")
	ada := w.member(t, alpha, "ada@alpha.test", "operator")
	const ownerNum, adaNum = "+2348011110041", "+2348011110042"
	w.bind(t, alpha, ownerNum)
	w.bind(t, ada, adaNum)
	alpha.must(200, "PUT", "/v1/whatsapp/number", map[string]any{"phone_number_id": pnA, "waba_id": "300000000000001", "access_token": tokA, "app_secret": secA, "verify_token": vtA})
	say := func(from, text string) []whatsapptest.Sent {
		return w.sendTo(t, "/channels/whatsapp/n/"+pnA, pnA, secA, from, whatsapptest.Text(text))
	}
	// The owner's own @mum, saved in the app: not found on the own number.
	alpha.must(201, "POST", "/v1/me/saved", map[string]any{"name": "mum", "title": "Mum", "bank_name": "Guaranty Trust Bank", "account_number": "0123452222"})
	if got := say(ownerNum, "@mum"); len(got) == 1 && strings.Contains(got[0].Text, "••2222") {
		t.Fatalf("one's own looked up on the own number: %+v", got)
	}
	// A save there is the business's only: no Keep it to myself.
	got := say(ownerNum, "save bayo: Zenith 2034567891")
	if len(got) != 1 {
		t.Fatalf("save: %+v", got)
	}
	for _, b := range got[0].Buttons {
		if b.ID == "mine" {
			t.Fatalf("keep it to myself offered: %+v", got[0])
		}
	}
	if got := say(ownerNum, "mine"); len(got) != 1 || !strings.Contains(got[0].Text, "On this number only Alpha's saved things") {
		t.Fatalf("mine on the own number: %+v", got)
	}
	// One without saved.manage: told, nothing kept.
	if got := say(adaNum, "save sis: UBA 2034567892"); len(got) != 1 || !strings.Contains(got[0].Text, "Keep your own in the Taskiem app") {
		t.Fatalf("a person's own on the own number: %+v", got)
	}

	// Elsewhere (the web chat), a person's own save waits sealed under
	// their key.
	if said := taskiemSays(t, ada, "save sis: UBA 2034567892"); !strings.Contains(said, "Save this as @sis?") {
		t.Fatalf("web save: %q", said)
	}
	var data string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT data::text FROM chat_sessions WHERE user_id = (SELECT id FROM users WHERE email = 'ada@alpha.test') AND data ? 'save'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, `"person"`) || strings.Contains(data, "$pii") || strings.Contains(data, "2034567892") {
		t.Fatalf("the waiting save: %s", data)
	}
	if said := taskiemSays(t, ada, "yes"); !strings.Contains(said, "Saved as @sis") {
		t.Fatalf("saved: %q", said)
	}
	if n := len(ada.must(200, "GET", "/v1/me/saved", nil)["saved"].([]any)); n != 1 {
		t.Fatalf("ada's own: %d", n)
	}
}
