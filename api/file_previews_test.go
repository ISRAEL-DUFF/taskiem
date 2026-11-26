package api_test

import (
	"context"
	"os"
	"strings"
	"testing"
)

// testFile is one of the media package's test files (a PDF of three
// pages, a protected one, Office documents).
func testFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../engine/media/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// lastMessage is the newest message of a list answered under key.
func lastMessage(t *testing.T, c *client, path, key string) map[string]any {
	t.Helper()
	list := c.must(200, "GET", path, nil)[key].([]any)
	return list[len(list)-1].(map[string]any)
}

// A PDF between linked businesses (docs/spec/chat-previews-and-editing.md
// §1): its first page is drawn after it is sent, once, and kept in each
// business's own storage; ?preview=1 answers it on each side, and not to
// a business outside the link; ?view=1 answers the PDF to read inside
// Taskiem, sandboxed. A Word file is a document, always downloaded; an
// older PDF with no picture is drawn when first asked for; a protected
// one has none.
func TestFilePreviewsLinkChat(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	store := newProjectStorage(t)
	w.srv.Media = store.service()
	w.srv.Media.Scanner = chatScanner{}
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)

	code, out := seller.sendFile(t, link, "price list.pdf", testFile(t, "three.pdf"), map[string]string{"text": "Our prices"})
	if code != 201 {
		t.Fatalf("a PDF: %d %v", code, out)
	}
	id := out["message"].(map[string]any)["id"].(string)
	w.srv.WaitBackground()
	for _, c := range []*client{seller, buyer} {
		m := lastMessage(t, c, "/v1/link-chats/"+link+"/messages", "messages")
		a := m["attachment"].(map[string]any)
		if a["type"] != "application/pdf" || a["kind"] != "file" || a["pages"] != float64(3) || a["preview"] != "ready" || a["w"] != float64(640) ||
			a["h"] == nil || a["preview_path"] != nil || a["path"] != nil || a["name"] != "price list.pdf" {
			t.Fatalf("the PDF as read: %v", a)
		}
		code, hdr, data := c.getRaw(t, "/v1/link-chats/"+link+"/files/"+id+"?preview=1")
		if code != 200 || hdr.Get("Content-Type") != "image/webp" || hdr.Get("X-Content-Type-Options") != "nosniff" || len(data) == 0 || len(data) > 200<<10 {
			t.Fatalf("the picture: %d %v %d", code, hdr, len(data))
		}
	}
	for _, project := range []string{"prj_" + sellerID, "prj_" + buyerID} {
		n := 0
		for _, p := range store.in(project) {
			if strings.HasPrefix(p, "chat/preview-") && strings.HasSuffix(p, ".webp") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%s keeps %d pictures: %v", project, n, store.in(project))
		}
	}
	stranger := w.tenant(t, "Stranger Ltd", "s@previews.test")
	if code, _, _ := stranger.getRaw(t, "/v1/link-chats/"+link+"/files/"+id+"?preview=1"); code != 404 {
		t.Fatalf("a stranger's picture: %d", code)
	}
	// The file itself: a download; ?view=1 inside Taskiem, sandboxed.
	code, hdr, _ := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+id)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Disposition"), "attachment") {
		t.Fatalf("the PDF downloaded: %d %v", code, hdr)
	}
	code, hdr, data := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+id+"?view=1")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Disposition"), "inline") || hdr.Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(hdr.Get("Content-Security-Policy"), "sandbox; default-src 'none'") || hdr.Get("X-Content-Type-Options") != "nosniff" ||
		hdr.Get("Cross-Origin-Resource-Policy") != "same-origin" || !strings.HasPrefix(string(data), "%PDF-") {
		t.Fatalf("the PDF to view: %d %v", code, hdr)
	}

	// A Word file: a document, its real name, always a download.
	code, out = seller.sendFile(t, link, "order.pdf", testFile(t, "note.docx"), nil)
	if code != 201 {
		t.Fatalf("a Word file: %d %v", code, out)
	}
	wm := out["message"].(map[string]any)
	if a := wm["attachment"].(map[string]any); a["doc"] != "word" || a["name"] != "order.docx" || a["kind"] != "file" || a["preview"] != nil {
		t.Fatalf("the Word file: %v", a)
	}
	for _, q := range []string{"", "?view=1"} {
		code, hdr, _ := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+wm["id"].(string)+q)
		if code != 200 || !strings.HasPrefix(hdr.Get("Content-Disposition"), "attachment") || hdr.Get("X-Content-Type-Options") != "nosniff" ||
			!strings.Contains(hdr.Get("Content-Type"), "wordprocessingml") {
			t.Fatalf("the Word file%s: %d %v", q, code, hdr)
		}
	}
	if code, _, _ := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+wm["id"].(string)+"?preview=1"); code != 404 {
		t.Fatalf("a Word file's picture: %d", code)
	}
	// Refused: macros, a password, a program, whatever their names.
	for name, data := range map[string][]byte{
		"protected.docx": testFile(t, "protected.docx"),
		"invoice.pdf":    append([]byte("MZ\x90\x00\x03"), make([]byte, 64)...),
	} {
		if code, out := seller.sendFile(t, link, name, data, nil); code != 400 && code != 422 {
			t.Fatalf("%s: %d %v", name, code, out)
		}
	}

	// A protected PDF: no picture, said to be protected.
	code, out = seller.sendFile(t, link, "locked.pdf", testFile(t, "protected.pdf"), nil)
	if code != 201 {
		t.Fatalf("a protected PDF: %d %v", code, out)
	}
	w.srv.WaitBackground()
	pa := lastMessage(t, buyer, "/v1/link-chats/"+link+"/messages", "messages")["attachment"].(map[string]any)
	if pa["protected"] != true || pa["preview"] != "none" || pa["pages"] != nil {
		t.Fatalf("the protected PDF: %v", pa)
	}
	if code, _, _ := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+out["message"].(map[string]any)["id"].(string)+"?preview=1"); code != 404 {
		t.Fatalf("a protected PDF's picture: %d", code)
	}

	// An older PDF, sent before pictures were drawn: it reads as before,
	// and its picture is drawn the first time someone asks for it.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE link_messages SET attachment = attachment - 'pages' - 'preview' - 'w' - 'h' - 'preview_path' WHERE pair_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	old := lastMessageWith(t, buyer, "/v1/link-chats/"+link+"/messages", id)
	if a := old["attachment"].(map[string]any); a["preview"] != nil || a["pages"] != nil || a["type"] != "application/pdf" || a["name"] != "price list.pdf" {
		t.Fatalf("the older PDF: %v", a)
	}
	if code, hdr, _ := buyer.getRaw(t, "/v1/link-chats/"+link+"/files/"+id+"?preview=1"); code != 200 || hdr.Get("Content-Type") != "image/webp" {
		t.Fatalf("the older PDF's picture: %d %v", code, hdr)
	}
	if a := lastMessageWith(t, buyer, "/v1/link-chats/"+link+"/messages", id)["attachment"].(map[string]any); a["preview"] != "ready" || a["pages"] != float64(3) {
		t.Fatalf("the older PDF once drawn: %v", a)
	}
}

// lastMessageWith is the message id of a list.
func lastMessageWith(t *testing.T, c *client, path, id string) map[string]any {
	t.Helper()
	for _, m := range c.must(200, "GET", path, nil)["messages"].([]any) {
		if mm := m.(map[string]any); mm["id"] == id {
			return mm
		}
	}
	t.Fatalf("no message %s in %s", id, path)
	return nil
}

// A PDF in a people chat (§1): each person's copy gets its picture,
// sealed under their own key, answered to each with ?preview=1 and to
// no one else; delete for everyone takes it.
func TestFilePreviewsPeopleChat(t *testing.T) {
	ctx := context.Background()
	w := newPeopleWorld(t)
	ada, _ := w.person(t, "+2348031170001", "Ada Obi")
	bola, bolaID := w.person(t, "+2348031170002", "Bola Ade")
	eve, _ := w.person(t, "+2348031170003", "Eve Ode")
	adaChat := chatWith(t, ada, bolaID)
	send(t, ada, adaChat, "Hello Bola")
	bolaChat := requestOf(t, bola)
	bola.must(200, "POST", "/v1/me/people/"+bolaChat+"/accept", nil)

	code, f := ada.postFile(t, "/v1/me/people/"+adaChat+"/files", "invoice.pdf", testFile(t, "three.pdf"), nil)
	if code != 201 || f["attachment"].(map[string]any)["type"] != "application/pdf" {
		t.Fatalf("a PDF: %d %v", code, f)
	}
	id := f["id"].(string)
	w.srv.WaitBackground()
	for _, c := range []struct {
		c    *client
		chat string
	}{{ada, adaChat}, {bola, bolaChat}} {
		ms := messagesOf(t, c.c, c.chat)
		a := ms[len(ms)-1]["attachment"].(map[string]any)
		if a["pages"] != float64(3) || a["preview"] != "ready" || a["w"] != float64(640) || a["name"] != "invoice.pdf" {
			t.Fatalf("the PDF as read: %v", a)
		}
		if code, hdr, data := c.c.getRaw(t, "/v1/me/people/"+c.chat+"/files/"+id+"?preview=1"); code != 200 || hdr.Get("Content-Type") != "image/webp" || len(data) == 0 {
			t.Fatalf("the picture: %d %v", code, hdr)
		}
	}
	// Sealed: no picture is kept in the clear.
	var kept, plain int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE position('WEBP' in sealed) > 0) FROM people_files WHERE message_id = $1 AND kind = 'preview'`, id).Scan(&kept, &plain); err != nil || kept != 2 || plain != 0 {
		t.Fatalf("the pictures sealed: %d %d %v", kept, plain, err)
	}
	if code, _, _ := eve.getRaw(t, "/v1/me/people/"+adaChat+"/files/"+id+"?preview=1"); code != 404 {
		t.Fatalf("someone outside the chat: %d", code)
	}
	if code, hdr, _ := bola.getRaw(t, "/v1/me/people/"+bolaChat+"/files/"+id+"?view=1"); code != 200 || !strings.HasPrefix(hdr.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("to view: %d %v", code, hdr)
	}
	// A spreadsheet: a document, no picture.
	code, x := bola.postFile(t, "/v1/me/people/"+bolaChat+"/files", "stock.xlsx", testFile(t, "sheet.xlsx"), nil)
	if code != 201 || x["attachment"].(map[string]any)["doc"] != "excel" {
		t.Fatalf("a spreadsheet: %d %v", code, x)
	}
	// Delete for everyone: the pictures go with the file.
	ada.must(204, "DELETE", "/v1/me/people/"+adaChat+"/messages/"+id+"?for=everyone", nil)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM people_files WHERE message_id = $1`, id).Scan(&kept); err != nil || kept != 0 {
		t.Fatalf("kept after delete for everyone: %d %v", kept, err)
	}
	if code, _, _ := bola.getRaw(t, "/v1/me/people/"+bolaChat+"/files/"+id+"?preview=1"); code != 404 {
		t.Fatalf("the picture after delete for everyone: %d", code)
	}
}

// A PDF in a group (§1): the group's one copy gets its picture, sealed
// under the group's key; members read it, others do not; an older one is
// drawn by the first member who asks; delete for everyone takes it.
func TestFilePreviewsGroup(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031171001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031171002", "Bola Ade")
	outsider, _ := w.many(t, "+2348031171003", "Out Sider")
	adds(t, ada, bola)
	adds(t, bola, ada)
	gid, _ := newGroup(t, ada, "Street", bolaID)

	code, f := ada.postFile(t, "/v1/me/groups/"+gid+"/files", "minutes.pdf", testFile(t, "three.pdf"), nil)
	if code != 201 {
		t.Fatalf("a PDF: %d %v", code, f)
	}
	id := f["id"].(string)
	w.srv.WaitBackground()
	a := lastMessage(t, bola, "/v1/me/groups/"+gid+"/messages", "messages")["attachment"].(map[string]any)
	if a["pages"] != float64(3) || a["preview"] != "ready" {
		t.Fatalf("the PDF as Bola reads it: %v", a)
	}
	if code, hdr, _ := bola.getRaw(t, "/v1/me/groups/"+gid+"/files/"+id+"?preview=1"); code != 200 || hdr.Get("Content-Type") != "image/webp" {
		t.Fatalf("Bola's picture: %d %v", code, hdr)
	}
	if code, _, _ := outsider.getRaw(t, "/v1/me/groups/"+gid+"/files/"+id+"?preview=1"); code != 404 {
		t.Fatalf("an outsider's picture: %d", code)
	}
	// An older one, with no picture: Bola, a member, draws it by asking.
	if _, err := w.env.DB.Admin.Exec(ctx, `DELETE FROM group_files WHERE message_id = $1 AND kind = 'preview'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_messages SET attachment = attachment - 'pages' - 'preview' - 'w' - 'h' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := bola.getRaw(t, "/v1/me/groups/"+gid+"/files/"+id+"?preview=1"); code != 200 {
		t.Fatalf("the older PDF's picture: %d", code)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM group_files WHERE message_id = $1 AND kind = 'preview'`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the older PDF's picture kept: %d %v", n, err)
	}
	ada.must(204, "DELETE", "/v1/me/groups/"+gid+"/messages/"+id+"?for=everyone", nil)
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM group_files WHERE message_id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("kept after delete for everyone: %d %v", n, err)
	}
}
