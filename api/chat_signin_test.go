package api_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// signInToken is the one-time token in a chat's "sign in" answer, or "".
func signInToken(reply string) string {
	if m := regexp.MustCompile(`#token=(\S+)`).FindStringSubmatch(reply); m != nil {
		return m[1]
	}
	return ""
}

// resetsOf counts the password reset links issued to a person.
func resetsOf(t *testing.T, w *world, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM password_resets WHERE user_id = $1`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// "sign in" in chat (N6) is the way into the web app for someone with no
// other: no password yet, or one on an address that receives no mail. A
// member who signs in by email resets by email, so holding their phone is
// not enough to take over their web account (F17).
func TestChatSignInLinkOnlyWithoutAnotherWay(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	chatSignupOn(w.world, "whatsapp")
	owner := w.tenant(t, "Acme", "owner@acme.test")
	ada := w.member(t, owner, "ada@acme.test", "operator")
	const adaNum, n1 = "+2348031235401", "+2348031235402"
	w.bind(t, ada, adaNum)
	w.say(t, adaNum, "I agree")

	// A web member with a password: pointed to the web, no link.
	r := w.say(t, adaNum, "sign in")
	if signInToken(r) != "" || !strings.Contains(r, waPublicURL+"/login") {
		t.Fatalf("a member with a password: %q", r)
	}
	if n := resetsOf(t, w.world, userID(t, ada)); n != 0 {
		t.Fatalf("reset links for a member with a password: %d", n)
	}

	// A chat-born owner (no password, a placeholder address) gets the
	// link, and still does once they have chosen a password.
	for _, m := range []string{"start", "yes", "Ada Foods", "Ada", "1", "1"} {
		w.say(t, n1, m)
	}
	born, _ := w.wa.UserOf(ctx, n1)
	if born == uuid.Nil {
		t.Fatal("no chat-born account")
	}
	tok := signInToken(w.say(t, n1, "sign in"))
	if tok == "" {
		t.Fatal("no link for a chat-born owner")
	}
	anon := &client{t: t, base: w.base}
	anon.must(200, "POST", "/v1/auth/password/reset", map[string]any{"token": tok, "password": "ada-web-password-1"})
	if signInToken(w.say(t, n1, "sign in")) == "" {
		t.Fatal("no link for a chat-born owner with a password")
	}
}
