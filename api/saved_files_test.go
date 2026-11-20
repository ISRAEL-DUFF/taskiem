package api_test

import (
	"context"
	"testing"
)

// Saved files (saved objects S1): a person's own photo (cleaned, as a
// chat's photo is: a JPEG) saved as a file
// object, opened, and changed for another (the old one gone); an upload
// claimed once only; a business's file kept in its own storage and removed
// with the thing; a made-up kind's file field; an upload never saved gone
// a day on.
func TestSavedFiles(t *testing.T) {
	ctx := context.Background()
	w := newDoorShopWorld(t)
	ps := newProjectStorage(t)
	w.srv.Media = ps.service()
	w.srv.Media.Scanner = chatScanner{}
	tolu, _ := w.personIn(t, "+2348031120061")
	toluID := personID(t, tolu)
	upload := func(c *client, path string, shade uint8) map[string]any {
		t.Helper()
		code, out := c.postFile(t, path, "logo.png", pngPhoto(t, shade), nil)
		if code != 201 {
			t.Fatalf("upload: %d %v", code, out)
		}
		return out["upload"].(map[string]any)
	}
	stored := func() int {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		return len(ps.files)
	}

	// The person's own: kept with them, not in a business's storage.
	up := upload(tolu, "/v1/me/saved/uploads", 40)
	if up["type"] != "image/jpeg" || up["name"] != "logo.png" || stored() != 0 {
		t.Fatalf("the upload: %v (%d stored)", up, stored())
	}
	logo := tolu.must(201, "POST", "/v1/me/saved", map[string]any{"name": "logo", "title": "Our logo", "kind": "file", "details": map[string]any{"file": up["file"]}})["saved"].(map[string]any)
	if d := logo["details"].(map[string]any); d["file_name"] != "logo.png" || d["file_type"] != "image/jpeg" || logo["kind"] != "file" {
		t.Fatalf("the file object: %v", logo)
	}
	got := func(c *client, path string) (int, string) {
		t.Helper()
		code, hdr, body := c.rawGet(t, path)
		if len(body) > 3 {
			body = body[:3]
		}
		return code, hdr.Get("Content-Type") + ":" + body
	}
	if code, head := got(tolu, "/v1/me/saved/"+logo["id"].(string)+"/file"); code != 200 || head != "image/jpeg:\xff\xd8\xff" {
		t.Fatalf("opened: %d %q", code, head)
	}
	// Claimed once only.
	tolu.must(400, "POST", "/v1/me/saved", map[string]any{"name": "logo2", "title": "Again", "kind": "file", "details": map[string]any{"file": up["file"]}})
	// Changed for another: the old one gone.
	up2 := upload(tolu, "/v1/me/saved/uploads", 90)
	tolu.must(200, "PUT", "/v1/me/saved/"+logo["id"].(string), map[string]any{"details": map[string]any{"file": up2["file"]}})
	var rows int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_files WHERE person_id = $1`, toluID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("files kept: %d %v", rows, err)
	}
	// An upload never saved goes a day on.
	upload(tolu, "/v1/me/saved/uploads", 120)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE saved_files SET created_at = now() - interval '2 days' WHERE person_id = $1 AND object_id IS NULL`, toluID); err != nil {
		t.Fatal(err)
	}
	upload(tolu, "/v1/me/saved/uploads", 150)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM saved_files WHERE person_id = $1 AND object_id IS NULL`, toluID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("waiting uploads after a day: %d %v", rows, err)
	}

	// The business's: in its own storage, removed with the thing.
	bup := upload(w.owner, "/v1/saved/uploads", 60)
	if stored() != 1 {
		t.Fatalf("the business's storage: %d", stored())
	}
	menu := w.owner.must(201, "POST", "/v1/saved", map[string]any{"name": "menu", "title": "Menu", "kind": "file", "details": map[string]any{"file": bup["file"]}})["saved"].(map[string]any)
	if code, head := got(w.owner, "/v1/saved/"+menu["id"].(string)+"/file"); code != 200 || head != "image/jpeg:\xff\xd8\xff" {
		t.Fatalf("the business's opened: %d %q", code, head)
	}
	if said := taskiemSays(t, w.owner, "@menu"); said == "" {
		t.Fatal("@menu told nothing")
	}
	w.owner.must(204, "DELETE", "/v1/saved/"+menu["id"].(string), nil)
	if stored() != 0 {
		t.Fatalf("left in storage: %d", stored())
	}

	// A made-up kind's file field.
	k := tolu.must(201, "POST", "/v1/me/saved-kinds", map[string]any{"name": "Supplier", "fields": []map[string]any{
		{"label": "Account", "type": "bank_account"}, {"label": "Price list", "type": "file"}}})["kind"].(map[string]any)
	pl := upload(tolu, "/v1/me/saved/uploads", 200)
	sup := tolu.must(201, "POST", "/v1/me/saved", map[string]any{"name": "dangote", "title": "Dangote", "kind": "custom", "kind_id": k["id"],
		"values": map[string]any{"account": "Zenith Bank 0123456789", "price_list": pl["file"]}})["saved"].(map[string]any)
	if v := sup["values"].(map[string]any); v["account"] != "Zenith Bank ••6789" || sup["files"].(map[string]any)["price_list"].(map[string]any)["name"] != "logo.png" {
		t.Fatalf("the supplier: %v", sup)
	}
	if code, _ := got(tolu, "/v1/me/saved/"+sup["id"].(string)+"/file?field=price_list"); code != 200 {
		t.Fatalf("its price list: %d", code)
	}
	tolu.must(200, "PUT", "/v1/me/saved/"+sup["id"].(string), map[string]any{"values": map[string]any{"price_list": nil}})
	if code, _ := got(tolu, "/v1/me/saved/"+sup["id"].(string)+"/file?field=price_list"); code != 404 {
		t.Fatalf("the price list let go: %d", code)
	}
}
