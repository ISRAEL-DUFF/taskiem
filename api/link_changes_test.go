package api_test

import (
	"testing"
	"time"
)

// What changed in the conversations since a time (K3, docs/spec/mobile-
// app-explore-and-offline.md): for a phone that kept them while closed.
// With no since, nothing and where to begin; then what was written, read
// or reacted to since, and what was deleted for this side; never another
// business's.
func TestLinkChatChanges(t *testing.T) {
	w := newWAWorld(t)
	seller, buyer, _, _ := linkedPair(t, w)
	other := w.tenant(t, "Someone Else", "else@changes.test")
	link := buyer.must(200, "GET", "/v1/link-chats", nil)["conversations"].([]any)[0].(map[string]any)["link"].(string)

	start := buyer.must(200, "GET", "/v1/link-chats/changes", nil)
	if len(start["messages"].([]any)) != 0 || len(start["gone"].([]any)) != 0 || start["more"] != false {
		t.Fatalf("with no since: %v", start)
	}
	until, err := time.Parse(time.RFC3339Nano, start["until"].(string))
	if err != nil || time.Since(until) > time.Minute {
		t.Fatalf("until: %v %v", start["until"], err)
	}
	since := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)

	// Written by them, and one of ours reacted to: both in the changes.
	m := seller.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Rice is back"})["message"].(map[string]any)
	mine := buyer.must(201, "POST", "/v1/link-chats/"+link+"/messages", map[string]any{"text": "Two bags please"})["message"].(map[string]any)
	seller.must(200, "PUT", "/v1/link-chats/"+link+"/messages/"+mine["id"].(string)+"/reaction", map[string]any{"emoji": "👍"})
	got := buyer.must(200, "GET", "/v1/link-chats/changes?since="+since, nil)
	byID := map[string]map[string]any{}
	for _, x := range got["messages"].([]any) {
		byID[x.(map[string]any)["id"].(string)] = x.(map[string]any)
	}
	if byID[m["id"].(string)]["body"] != "Rice is back" || byID[mine["id"].(string)]["reactions"] == nil {
		t.Errorf("the changes: %v", got["messages"])
	}
	// Deleted for us: gone, not a message.
	buyer.must(204, "POST", "/v1/link-chats/"+link+"/messages/"+m["id"].(string)+"/hide", nil)
	got = buyer.must(200, "GET", "/v1/link-chats/changes?since="+since, nil)
	gone := got["gone"].([]any)
	if len(gone) != 1 || gone[0].(map[string]any)["id"] != m["id"] || gone[0].(map[string]any)["link"] != link {
		t.Errorf("gone: %v", gone)
	}
	for _, x := range got["messages"].([]any) {
		if x.(map[string]any)["id"] == m["id"] {
			t.Error("a message deleted for us is still sent")
		}
	}
	// Another business sees none of it.
	if o := other.must(200, "GET", "/v1/link-chats/changes?since="+since, nil); len(o["messages"].([]any)) != 0 || len(o["gone"].([]any)) != 0 {
		t.Errorf("another business's changes: %v", o)
	}
	buyer.must(400, "GET", "/v1/link-chats/changes?since=yesterday", nil)
}
