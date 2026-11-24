package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
)

// pressTool presses a page's button (a part's action, or a board's move),
// and runs the tool it opens with these values, through its Yes.
func pressTool(t *testing.T, w *tablesWorld, c *client, page string, body map[string]any, input map[string]any) {
	t.Helper()
	pressed := c.must(200, "POST", "/v1/pages/"+page+"/act?filters=%7B%7D", body)
	tool, ok := pressed["tool"].(map[string]any)
	if !ok || pressed["kind"] != "tool" {
		t.Fatalf("pressed: %v", pressed)
	}
	in := map[string]any{}
	if fixed, ok := tool["inputs"].(map[string]any); ok {
		for k, v := range fixed {
			in[k] = v
		}
	}
	for k, v := range input {
		in[k] = v
	}
	cmd := tool["command"].(string)
	start := c.must(200, "POST", "/v1/tools/"+cmd+"/start", map[string]any{"input": in, "button": tool["button"]})
	if start["confirm"] == nil {
		t.Fatalf("a tool that writes asks first: %v", start)
	}
	run := c.must(201, "POST", "/v1/tools/"+cmd+"/confirm", map[string]any{"confirm": start["confirm"], "input": in})["run_id"]
	w.env.Drain(t)
	var status string
	_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT status FROM runs WHERE id = $1`, run).Scan(&status)
	if status != "completed" {
		var errText *string
		_ = w.env.DB.Admin.QueryRow(context.Background(), `SELECT error::text FROM runs WHERE id = $1`, run).Scan(&errText)
		t.Fatalf("/%s's run: %s %v", cmd, status, errText)
	}
}

// G5: every starter installs its tables, tools and page together, published
// at once for an owner, and works: the page draws with no part to fix, its
// buttons open its own tools, and what they write shows. Tickets desk is a
// board a ticket moves on; Expenses this month a chart by week, its button
// the M6 recipe. Installed again, each name already taken is given the next
// free one, and that copy works too. Someone who may not make tables gets
// nothing made; someone who may not publish gets drafts, and why.
func TestPageStartersInstallAndWork(t *testing.T) {
	defer api.SetPageToolWait(0)()
	w := newTablesWorld(t)
	owner := w.tenant(t, "Acme", "owner@g5.test")

	list := owner.must(200, "GET", "/v1/page-starters", nil)["starters"].([]any)
	if len(list) != 7 {
		t.Fatalf("starters: %v", list)
	}
	for _, raw := range list {
		st := raw.(map[string]any)
		made := owner.must(201, "POST", "/v1/page-starters/"+st["id"].(string)+"/install", map[string]any{"publish": true})
		if made["published"] != true || len(made["tables"].([]any)) == 0 || len(made["tools"].([]any)) == 0 {
			t.Fatalf("%s: %v", st["id"], made)
		}
		for _, tool := range made["tools"].([]any) {
			if tool.(map[string]any)["state"] != "published" {
				t.Errorf("%s: %v", st["id"], tool)
			}
		}
		v := viewOf(t, owner, made["page"].(string), nil)
		for _, b := range v["blocks"].([]any) {
			m := b.(map[string]any)
			if m["broken"] != nil || m["error"] != nil || m["unavailable"] != nil {
				t.Errorf("%s: a part to fix: %v", st["id"], m)
			}
		}
	}

	// Tickets desk: New ticket, then the board moves it to Doing.
	pressTool(t, w, owner, "tickets", map[string]any{"block": "new", "action": 0}, map[string]any{"title": "Printer jam", "customer": "Bayo", "priority": "high"})
	board := blockOf(t, viewOf(t, owner, "tickets", nil), "board")
	if l := lanesOf(t, board); strings.Join(l["open"], ",") != "Printer jam" {
		t.Fatalf("a new ticket is open: %v", l)
	}
	if open := blockOf(t, viewOf(t, owner, "tickets", nil), "open"); open["value"] != float64(1) {
		t.Errorf("open: %v", open)
	}
	pressTool(t, w, owner, "tickets", map[string]any{"block": "board", "row": cardID(t, board, "Printer jam"), "action": 0, "to": "doing"}, nil)
	if l := lanesOf(t, blockOf(t, viewOf(t, owner, "tickets", nil), "board")); strings.Join(l["doing"], ",") != "Printer jam" || len(l["open"]) != 0 {
		t.Errorf("moved: %v", l)
	}

	// Expenses this month: the recipe logs one, and it shows in this week's bar.
	pressTool(t, w, owner, "expenses-month", map[string]any{"block": "log", "action": 0}, map[string]any{"what": "Diesel", "amount": 4500})
	v := viewOf(t, owner, "expenses-month", nil)
	weeks := blockOf(t, v, "weeks")
	pts := weeks["points"].([]any)
	if len(pts) != 5 || pts[4].(map[string]any)["value"] != float64(4500) || blockOf(t, v, "total")["value"] != float64(4500) {
		t.Errorf("expenses by week: %v %v", weeks, blockOf(t, v, "total"))
	}

	// Leave: asked, then approved by its row's button.
	pressTool(t, w, owner, "leave-requests", map[string]any{"block": "ask", "action": 0}, map[string]any{"who": "Ada", "starts": "2026-12-01", "ends": "2026-12-05"})
	list2 := blockOf(t, viewOf(t, owner, "leave-requests", nil), "list")
	row := list2["rows"].([]any)[0].(map[string]any)
	if row["title"] != "Ada" || row["state"].(map[string]any)["text"] != "pending" || len(row["actions"].([]any)) != 2 {
		t.Fatalf("leave: %v", row)
	}
	pressTool(t, w, owner, "leave-requests", map[string]any{"block": "list", "row": row["id"], "action": 0}, nil)
	if r := blockOf(t, viewOf(t, owner, "leave-requests", nil), "list")["rows"].([]any)[0].(map[string]any); r["state"].(map[string]any)["text"] != "approved" || r["actions"] != nil {
		t.Errorf("approved: %v", r)
	}

	// Installed again: tickets_2, /new-ticket-2, tickets-2, said so.
	again := owner.must(201, "POST", "/v1/page-starters/tickets-desk/install", map[string]any{"publish": true})
	if again["page"] != "tickets-2" || toJSON(again["tables"]) == "" || !strings.Contains(toJSON(again["tables"]), `"name":"tickets_2"`) ||
		!strings.Contains(toJSON(again["tools"]), `"asked":"new-ticket","changed":true,"command":"new-ticket-2"`) || again["published"] != true {
		t.Fatalf("again: %v", again)
	}
	pressTool(t, w, owner, "tickets-2", map[string]any{"block": "new", "action": 0}, map[string]any{"title": "Second desk"})
	if l := lanesOf(t, blockOf(t, viewOf(t, owner, "tickets-2", nil), "board")); strings.Join(l["open"], ",") != "Second desk" {
		t.Errorf("the second desk keeps to its own table: %v", l)
	}

	// Not for everyone: an operator may not make tables; an editor who may
	// not publish gets drafts and why.
	owner.must(201, "PUT", "/v1/roles/maker", map[string]any{"permissions": []string{"page.edit"}})
	operator := addMember(t, w.world, owner, "op", "operator")
	operator.must(403, "POST", "/v1/page-starters/waiting-list/install", map[string]any{"publish": true})
	maker := addMember(t, w.world, owner, "maker", "maker", "builder") // builder: table.edit and workflow.edit
	drafts := maker.must(201, "POST", "/v1/page-starters/waiting-list/install", map[string]any{"publish": true})
	if drafts["published"] != false || !strings.Contains(drafts["waiting"].(string), "publish") || drafts["page"] != "waiting-list-2" {
		t.Errorf("drafts: %v", drafts)
	}
	owner.must(404, "POST", "/v1/page-starters/nope/install", nil)
}
