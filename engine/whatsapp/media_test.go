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
