package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
)

// The fixes from the stage 5b load test ("As built: stage 5b, load test"):
// a group's member count through taskiem_group_count (00490) and the live
// connection's ticks woken by each group's newest mark, both without
// row-level security's check on every member's row. What each answers is
// unchanged: the count to someone who is or was in the group (0 to anyone
// else); a 256-member group's ticks wake the sender's connection once a
// mark moves, and the number of membership checks no longer grows with the
// group.
func TestGroupsLoadFixes(t *testing.T) {
	ctx := context.Background()
	w := newJoinedWorld(t)
	ada, _ := w.many(t, "+2348031149001", "Ada Obi")
	bola, bolaID := w.many(t, "+2348031149002", "Bola Ade")
	_, outsiderID := w.many(t, "+2348031149003", "Out Sider")
	adds(t, ada, bola)
	gid, _ := newGroup(t, ada, "Big", bolaID)
	bola.must(200, "POST", "/v1/me/groups/invites/"+gid+"/accept", nil)
	// 254 more, as TestGroupsFull fills a group.
	if _, err := w.env.DB.Admin.Exec(ctx, `WITH u AS (INSERT INTO users (id, email, name) SELECT gen_random_uuid(), gen_random_uuid()::text || '@x.test', 'Filler' FROM generate_series(1, 254) RETURNING id)
		INSERT INTO group_members (group_id, person_id, how, visible) SELECT $1, id, 'added', tstzmultirange(tstzrange(now(), NULL)) FROM u`, gid); err != nil {
		t.Fatal(err)
	}
	if g := ada.must(200, "GET", "/v1/me/groups/"+gid, nil); g["members"] != float64(256) {
		t.Fatalf("members: %v", g["members"])
	}
	count := func(person uuid.UUID) (n int) {
		t.Helper()
		if err := db.InPersonTx(ctx, w.env.DB.App, person, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT taskiem_group_count($1)`, gid).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(bolaID) != 256 || count(outsiderID) != 0 {
		t.Fatalf("count: bola %d, outsider %d", count(bolaID), count(outsiderID))
	}
	// Bola's ticks on her own message move when the others' marks do.
	events, _, closeB := liveStream(t, bola, "")
	defer closeB()
	m := gsend(t, bola, gid, "Ticks at 256")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE group_members SET delivered_upto = now() WHERE group_id = $1 AND person_id <> $2`, gid, bolaID); err != nil {
		t.Fatal(err)
	}
	if await(events, 8*time.Second, func(ev streamEvent) bool {
		if ev.Event != "group_receipts" || ev.Data["group_id"] != gid {
			return false
		}
		for _, x := range ev.Data["messages"].([]any) {
			if x.(map[string]any)["id"] == m["id"] && x.(map[string]any)["status"] == "delivered" {
				return true
			}
		}
		return false
	}) == nil {
		t.Fatal("no delivered tick")
	}
	// The ticks' wake-up reads one row per group, not the members whose
	// marks moved: its plan stops at the newest (a Limit under the max).
	if err := db.InPersonTx(ctx, w.env.DB.App, bolaID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `EXPLAIN (ANALYSE, FORMAT TEXT) SELECT g.id, x.at FROM unnest($1::uuid[]) AS g(id),
			LATERAL (SELECT max(m.marks_at) AS at FROM group_members m WHERE m.group_id = g.id AND m.person_id <> $2 AND m.marks_at > $3) x
			WHERE x.at IS NOT NULL ORDER BY x.at, g.id LIMIT 200`, []uuid.UUID{uuid.MustParse(gid)}, bolaID, time.Now().Add(-time.Hour))
		if err != nil {
			return err
		}
		plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, l := range plan {
			if strings.Contains(l, "Index Scan Backward using group_members_marks") && strings.Contains(l, "rows=1 loops") {
				return nil
			}
		}
		t.Fatalf("the ticks' wake-up reads more than the newest mark:\n%v", plan)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
