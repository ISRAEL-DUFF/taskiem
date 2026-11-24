package api

import (
	"fmt"
	"testing"
	"time"
)

// The cards kept: at most perKind of a kind and maxChatAlerts in all, the
// approvals never pushed out by a long list of one kind.
func TestCapAlerts(t *testing.T) {
	now := time.Now()
	var all []chatAlert
	for i := range 30 {
		all = append(all, chatAlert{Key: fmt.Sprint("low", i), Kind: "low_stock", At: now})
	}
	for i := range 3 {
		all = append(all, chatAlert{Key: fmt.Sprint("ap", i), Kind: "approval", At: now.Add(-time.Hour * time.Duration(i+1))})
	}
	for i := range 8 {
		all = append(all, chatAlert{Key: fmt.Sprint("ov", i), Kind: "overdue", At: now.Add(-time.Minute * time.Duration(i))})
	}
	got := capAlerts(all)
	n := map[string]int{}
	for _, a := range got {
		n[a.Kind]++
	}
	if n["approval"] != 3 || n["low_stock"] != perKind || n["overdue"] != perKind || len(got) != 13 {
		t.Fatalf("kept %v", n)
	}
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatal("not oldest first")
		}
	}
}

// Who asked: a person by their id only, never an id at the end of
// something else.
func TestActorUser(t *testing.T) {
	const id = "0193a7c4-1111-7222-8333-444455556666"
	for by, want := range map[string]bool{
		id: true, "user:" + id: true, "end_user:app/" + id: false, "connector:" + id: false, "workflow:" + id: false,
	} {
		if got := actorUser.MatchString(by); got != want {
			t.Errorf("%q: %v", by, got)
		}
	}
	if m := subflowOf.FindStringSubmatch("subflow:" + id + "/step"); m == nil || m[1] != id {
		t.Errorf("subflow: %v", m)
	}
}
