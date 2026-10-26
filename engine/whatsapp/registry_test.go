package whatsapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplateStatusWebhook(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"300000000000001","time":1759800000,"changes":[
	  {"field":"message_template_status_update","value":{"event":"APPROVED","message_template_id":1234567890123456789,"message_template_name":"taskiem_invoice","message_template_language":"en","reason":"NONE"}},
	  {"field":"message_template_status_update","value":{"event":"REJECTED","message_template_id":"42","message_template_name":"taskiem_receipt","message_template_language":"en","reason":"INVALID_FORMAT",
	    "rejection_info":{"reason":"Too many variables","recommendation":"Add more text around them"}}},
	  {"field":"message_template_status_update","value":{"event":"PAUSED","message_template_id":43,"message_template_name":"taskiem_alert","message_template_language":"en","reason":null,
	    "other_info":{"title":"FIRST_PAUSE","description":"Paused for 3 hours for low quality"}}},
	  {"field":"messages","value":{"metadata":{"phone_number_id":"111"},"messages":[]}}]}]}`)
	got, err := ParseTemplateStatuses(body)
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].WABA != "300000000000001" || got[0].Event != "APPROVED" || got[0].MetaID != "1234567890123456789" || got[0].Name != "taskiem_invoice" || got[0].Language != "en" || got[0].Reason != "" {
		t.Errorf("approved: %+v", got[0])
	}
	if got[1].MetaID != "42" || got[1].Reason != "INVALID_FORMAT; Too many variables: Add more text around them" {
		t.Errorf("rejected: %+v", got[1])
	}
	if got[2].MetaID != "43" || got[2].Reason != "FIRST_PAUSE: Paused for 3 hours for low quality" {
		t.Errorf("paused: %+v", got[2])
	}
	for in, want := range map[string]string{"APPROVED": TemplateApproved, "approved": TemplateApproved, "REINSTATED": TemplateApproved, "PENDING": TemplatePending, "IN_APPEAL": TemplatePending,
		"REJECTED": TemplateRejected, "PAUSED": TemplatePaused, "DISABLED": TemplateDisabled, "LOCKED": TemplateDisabled, "DELETED": TemplateNotSubmitted, "PENDING_DELETION": TemplateNotSubmitted} {
		if st, ok := StatusFromMeta(in); !ok || st != want {
			t.Errorf("%s: %s %v", in, st, ok)
		}
	}
	if _, ok := StatusFromMeta("FLAGGED"); ok {
		t.Error("a quality flag changes no status")
	}
	if n := NotApprovedNote([]TemplateStatus{{Name: "a", Status: TemplateApproved}, {Name: "b", Status: TemplatePending}}); !strings.Contains(n, "b") || strings.Contains(n, "a,") {
		t.Errorf("note %q", n)
	}
	if NotApprovedNote([]TemplateStatus{{Name: "a", Status: TemplateApproved}}) != "" {
		t.Error("note with all approved")
	}
}

func TestBusinessManagementCalls(t *testing.T) {
	var created map[string]any
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || !strings.HasPrefix(r.URL.Path, "/300000000000001/message_templates") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = w.Write([]byte(`{"id":"1234567890123456789","status":"PENDING","category":"UTILITY"}`))
			return
		}
		pages++
		if r.URL.Query().Get("after") == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"taskiem_invoice","language":"en","status":"APPROVED","category":"UTILITY"}],"paging":{"cursors":{"before":"a","after":"c2"},"next":"` + r.Host + `/next"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"2","name":"taskiem_receipt","language":"en","status":"REJECTED","category":"UTILITY","rejected_reason":"INVALID_FORMAT"}],"paging":{"cursors":{"before":"c2","after":"c3"}}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, PhoneNumberID: "111", Token: "tok", HTTP: srv.Client()}
	def, _ := TplInvoice.Submission("en", Hosts{Forms: "https://forms.example.com"})
	id, status, err := c.CreateTemplate(context.Background(), "300000000000001", def)
	if err != nil || id != "1234567890123456789" || status != "PENDING" || created["name"] != "taskiem_invoice" {
		t.Fatalf("create: %s %s %v %v", id, status, err, created)
	}
	listed, err := c.ListTemplates(context.Background(), "300000000000001")
	if err != nil || len(listed) != 2 || pages != 2 || listed[0].ID != "1" || listed[0].Status != "APPROVED" || listed[1].Reason() != "INVALID_FORMAT" {
		t.Fatalf("list: %+v %v (%d pages)", listed, err, pages)
	}
	if _, _, err := c.CreateTemplate(context.Background(), "not-an-id", def); err == nil {
		t.Error("a bad account id")
	}
}
