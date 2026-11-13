package api_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// A rider holding two orders sends a photo: it is kept for the order they
// last marked arrived or delivered, not one they have only picked up;
// when two fit, a button moves it to the other; and a later photo is kept
// beside the earlier one, not in its place.
func TestProofPhotoGoesToTheRightOrder(t *testing.T) {
	w, ada := newDispatchWorld(t, "Ada Stores")
	ctx := context.Background()
	const musa = "+2348031110001"
	path := strings.TrimPrefix(ada.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc, _ := url.Parse(w.page(t, path+"/whatsapp", nil).Header().Get("Location"))
	w.whatsapp(t, musa, loc.Query().Get("text"))
	rider := uuid.MustParse(ada.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}})["rider"].(map[string]any)["id"].(string))

	take := func(ref string, taps ...string) uuid.UUID {
		t.Helper()
		out, err := w.srv.Dispatch.Offer(ctx, w.biz, ref, dispatch.OfferIn{Rider: &rider, By: "user:ada"})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range taps {
			if _, err := w.srv.Dispatch.Press(ctx, w.biz, out.Delivery.ID, a); err != nil {
				t.Fatalf("%s %s: %v", ref, a, err)
			}
		}
		return out.Delivery.ID
	}
	photo := func() []whatsapptest.Sent {
		t.Helper()
		id := fmt.Sprintf("media-%d", w.seq.Add(1))
		w.graph.AddMedia(id, "image/jpeg", []byte("\xff\xd8\xff "+id), 0)
		body := whatsapptest.Delivery(waPNID, musa, fmt.Sprintf("wamid.img%d", w.seq.Add(1)), whatsapptest.Image(id, "image/jpeg", ""))
		before := len(w.graph.Sent(musa))
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
		return w.graph.Sent(musa)[before:]
	}
	proofs := func(delivery uuid.UUID) (n int, latest string) {
		t.Helper()
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM delivery_proofs WHERE delivery_id = d.id), COALESCE(d.proof, '') FROM deliveries d WHERE d.id = $1`,
			delivery).Scan(&n, &latest); err != nil {
			t.Fatal(err)
		}
		return n, latest
	}

	// A is delivered; B is only picked up, and the later of the two.
	refA, refB := w.order(t, "none"), w.order(t, "none")
	a := take(refA, dispatch.Accept, dispatch.PickedUp, dispatch.Arrived, dispatch.Delivered)
	b := take(refB, dispatch.Accept, dispatch.PickedUp)
	w.tick(t)
	if got := photo(); len(got) != 1 || !strings.Contains(got[0].Text, refA) || len(got[0].Buttons) != 0 {
		t.Fatalf("the photo's answer: %+v", got)
	}
	if n, _ := proofs(a); n != 1 {
		t.Fatalf("the delivered order has %d photos", n)
	}
	if n, latest := proofs(b); n != 0 || latest != "" {
		t.Fatalf("the order only picked up has %d photos (%q)", n, latest)
	}

	// B arrives: two fit now. The photo goes to B, the last marked, and a
	// button offers A.
	if _, err := w.srv.Dispatch.Press(ctx, w.biz, b, dispatch.Arrived); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	got := photo()
	if len(got) != 1 || !strings.Contains(got[0].Text, refB) || len(got[0].Buttons) != 1 || got[0].Buttons[0].Title != refA {
		t.Fatalf("with two orders at the door: %+v", got)
	}
	if n, _ := proofs(b); n != 1 {
		t.Fatalf("the order at the door has %d photos", n)
	}
	// It was for A: the button moves it, and A keeps its first photo too.
	_, first := proofs(a)
	moved := w.press(t, musa, got[0].Buttons[0].ID)
	if len(moved) != 1 || !strings.Contains(moved[0].Text, refA) {
		t.Fatalf("moved: %+v", moved)
	}
	n, latest := proofs(a)
	if nb, lb := proofs(b); n != 2 || latest == first || nb != 0 || lb != "" {
		t.Fatalf("after moving: A has %d photos (latest %q, first %q), B %d (%q)", n, latest, first, nb, lb)
	}
	// The owner sees both of A's photos.
	ds := ada.must(200, "GET", "/v1/storefront/orders/"+refA+"/deliveries", nil)["deliveries"].([]any)
	if all, _ := ds[0].(map[string]any)["proofs"].(float64); all != 2 {
		t.Fatalf("the order's delivery: %v", ds[0])
	}
	// The button works once.
	if again := w.press(t, musa, got[0].Buttons[0].ID); len(again) != 1 || !strings.Contains(again[0].Text, "no longer works") {
		t.Fatalf("pressed again: %+v", again)
	}
}
