package whatsapp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

func TestParseAudio(t *testing.T) {
	body := whatsapptest.Delivery("PN", "+2348010000001", "wamid.a", whatsapptest.Audio("media-1", "audio/ogg; codecs=opus", true))
	ins, err := whatsapp.Parse(body)
	if err != nil || len(ins) != 1 {
		t.Fatal(ins, err)
	}
	m := ins[0].Media
	if ins[0].Type != "audio" || ins[0].Text != "" || m == nil || m.ID != "media-1" || m.MimeType != "audio/ogg; codecs=opus" || !m.Voice {
		t.Fatalf("%+v %+v", ins[0], m)
	}
}

func TestParseImage(t *testing.T) {
	body := whatsapptest.Delivery("PN", "+2348010000001", "wamid.i", whatsapptest.Image("media-2", "image/jpeg", "Jollof rice ₦2,000"))
	ins, err := whatsapp.Parse(body)
	if err != nil || len(ins) != 1 {
		t.Fatal(ins, err)
	}
	in := ins[0]
	if in.Type != "image" || in.Text != "Jollof rice ₦2,000" || in.Media != nil || in.Image == nil || in.Image.ID != "media-2" || in.Image.MimeType != "image/jpeg" {
		t.Fatalf("%+v %+v", in, in.Image)
	}
	// Without a caption, and an image without an id (nothing to fetch).
	ins, _ = whatsapp.Parse(whatsapptest.Delivery("PN", "+2348010000001", "wamid.j", whatsapptest.Image("media-3", "image/png", "")))
	if len(ins) != 1 || ins[0].Text != "" || ins[0].Image == nil {
		t.Fatalf("%+v", ins)
	}
	ins, _ = whatsapp.Parse(whatsapptest.Delivery("PN", "+2348010000001", "wamid.k", map[string]any{"type": "image", "image": map[string]any{"caption": "x"}}))
	if len(ins) != 1 || ins[0].Image != nil {
		t.Fatalf("%+v", ins)
	}
}

func TestSendImage(t *testing.T) {
	g := whatsapptest.New(t, "PN", "tok")
	c := &whatsapp.Client{BaseURL: g.URL, PhoneNumberID: "PN", Token: "tok", HTTP: g.Client()}
	ctx := context.Background()
	if _, err := c.SendImage(ctx, "+2348010000001", "https://ada.shops.test/qr.png", "https://ada.shops.test"); err != nil {
		t.Fatal(err)
	}
	if s := g.Last(t, "+2348010000001"); s.Type != "image" || s.Image != "https://ada.shops.test/qr.png" || s.Text != "https://ada.shops.test" {
		t.Fatalf("%+v", s)
	}
	if _, err := c.SendImage(ctx, "+2348010000001", "http://ada.shops.test/qr.png", ""); err == nil {
		t.Error("a link that is not https")
	}
}

func TestMediaDownload(t *testing.T) {
	g := whatsapptest.New(t, "PN", "tok")
	g.AddMedia("m1", "audio/ogg", []byte("OggS fake audio"), 0)
	c := &whatsapp.Client{BaseURL: g.URL, PhoneNumberID: "PN", Token: "tok", HTTP: g.Client()}
	ctx := context.Background()
	mi, err := c.MediaInfo(ctx, "m1")
	if err != nil || mi.MimeType != "audio/ogg" || mi.Size != 15 || !strings.HasPrefix(mi.URL, g.URL) {
		t.Fatalf("%+v %v", mi, err)
	}
	data, err := c.Download(ctx, mi, 1024)
	if err != nil || string(data) != "OggS fake audio" {
		t.Fatalf("%q %v", data, err)
	}
	// Caps: by the reported size, and by what actually arrives.
	if _, err := c.Download(ctx, mi, 10); !errors.Is(err, whatsapp.ErrMediaTooLarge) {
		t.Errorf("reported size: %v", err)
	}
	lied := mi
	lied.Size = 1
	if _, err := c.Download(ctx, lied, 10); !errors.Is(err, whatsapp.ErrMediaTooLarge) {
		t.Errorf("real size: %v", err)
	}
	// A digest that does not match is refused.
	bad := mi
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := c.Download(ctx, bad, 1024); err == nil {
		t.Error("digest mismatch accepted")
	}
	// Only Meta's media host (or the Graph API's own, for fakes).
	for _, u := range []string{"https://evil.example/m1", "http://lookaside.fbsbx.com/x", "https://169.254.169.254/latest"} {
		other := mi
		other.URL = u
		if _, err := c.Download(ctx, other, 1024); err == nil || !strings.Contains(err.Error(), "unexpected host") {
			t.Errorf("%s: %v", u, err)
		}
	}
	// Unknown id, wrong token.
	if _, err := c.MediaInfo(ctx, "nope"); err == nil {
		t.Error("unknown media id")
	}
	if _, err := (&whatsapp.Client{BaseURL: g.URL, Token: "wrong", HTTP: g.Client()}).MediaInfo(ctx, "m1"); err == nil {
		t.Error("wrong token")
	}
	if _, err := c.MediaInfo(ctx, "../PN/messages"); err == nil {
		t.Error("path in a media id")
	}
}
