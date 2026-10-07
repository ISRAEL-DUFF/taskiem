package africastalking_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/africastalking"
	"github.com/israel-duff/taskiem/engine/ussd"
)

// The callback as Africa's Talking documents it: a form post with
// sessionId, serviceCode, phoneNumber, networkCode and text; the answer
// is plain text starting CON or END.
func TestUSSDCallbackFormat(t *testing.T) {
	a := africastalking.USSDAdapter
	if a.Name() != "africastalking" || a.Incremental() {
		t.Fatal("adapter identity")
	}
	body := "sessionId=ATUid_9f2&serviceCode=%2A384%2A123%23&phoneNumber=%2B254711000001&networkCode=63902&text=1%2A0123456789"
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	q, err := a.Parse(r, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := ussd.Request{SessionID: "ATUid_9f2", ServiceCode: "*384*123#", Phone: "+254711000001", Network: "63902", Input: "1*0123456789"}
	if q != want {
		t.Fatalf("parsed %+v", q)
	}
	if err := ussd.CheckRequest(q); err != nil {
		t.Fatal(err)
	}
	// A number without its plus sign is normalised.
	q, _ = a.Parse(r, []byte("sessionId=s&serviceCode=*1%23&phoneNumber=254711000001&text="))
	if q.Phone != "+254711000001" || q.Input != "" {
		t.Fatalf("parsed %+v", q)
	}
	r.Header.Set("Content-Type", "application/json")
	if _, err := a.Parse(r, []byte(`{}`)); err == nil {
		t.Fatal("accepted JSON")
	}
	for _, end := range []bool{false, true} {
		w := httptest.NewRecorder()
		a.Reply(w, end, "Welcome\n1. Pay")
		want := "CON Welcome\n1. Pay"
		if end {
			want = "END Welcome\n1. Pay"
		}
		if w.Code != http.StatusOK || w.Body.String() != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("reply: %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
		}
	}
}
