package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/pages"
)

// Pages in the chat (docs/spec/pages.md §9, G3): a page cannot be drawn on
// WhatsApp, Telegram or Slack, so its name ("tickets", or "/tickets")
// answers with its short form: its figures on a line, the first five rows
// of its first list (or table), and a link to open it. Taskiem's own words
// are read first (a page's name is never one, nor a tool's command), then
// the business's tools, then its pages. Only a published page the person's
// roles let them open, drawn for them as the page is (a personal column
// masked without table.read, Taskiem's records only with the area's
// permission); anyone else is answered as if there were no page.

// pageShortRows is how many rows the short form shows.
const pageShortRows = 5

// waPage answers a page's name with its short form; false when the message
// names no page this person may open.
func (s *Server) waPage(ctx context.Context, c *chat, text string) (bool, error) {
	if c.state != stateIdle || c.in.Reply != "" || c.p == nil {
		return false, nil
	}
	words := strings.Fields(text)
	if len(words) != 1 {
		return false, nil
	}
	name := strings.ToLower(strings.TrimPrefix(words[0], "/"))
	if pages.ReservedName(name) {
		return false, nil
	}
	short, ok, err := s.pageShortForm(context.WithValue(ctx, ctxKey{}, c.p), name, c.t)
	if err != nil || !ok {
		return false, err
	}
	return true, c.say(ctx, s, short)
}

// pageShortForm is a page's short form for the principal in ctx: false
// when there is no such published page or they may not open it.
func (s *Server) pageShortForm(ctx context.Context, name string, t func(id string, kv ...string) string) (string, bool, error) {
	p := principalFrom(ctx)
	pc := &pageCtx{broken: map[int]string{}, off: map[int]string{}}
	var viewer pages.Viewer
	found := false
	err := db0(ctx, s, p.TenantID, func(tx pgx.Tx) error {
		pg, err := loadPage(ctx, tx, name)
		if errors.Is(err, errNoPage) {
			return nil
		}
		if err != nil {
			return err
		}
		// Published, and the roles let them in (an editor's drafts are the web's).
		if pg.Version == nil || pg.Published == nil || !mayStart(p, pg.Roles) {
			return nil
		}
		page, _ := pages.Parse(pg.Published)
		if page == nil {
			return nil
		}
		pc.row, pc.page, found = pg, page, true
		if pc.tables, err = datatables.ListTables(ctx, tx); err != nil {
			return err
		}
		areaOn, err := pageAreas(ctx, tx)
		if err != nil {
			return err
		}
		for i, b := range page.Blocks {
			if v, ok := pages.RecordViewNamed(recordsOf(b)); ok && !areaOn(string(v.Area)) {
				pc.off[i] = v.Unavailable()
			}
		}
		env := pages.Env{Tables: pc.tables, AreaOn: areaOn}
		pageEnvTools(ctx, s, tx, &env)
		problems := pages.Check(page, env)
		for i := range page.Blocks {
			if blockHasProblem(problems, i) {
				pc.broken[i] = "broken"
			}
		}
		if p.UserID != uuid.Nil {
			var email *string
			_ = tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, p.UserID).Scan(&email)
			if email != nil {
				viewer.Email = *email
			}
		}
		return nil
	})
	if err != nil || !found {
		return "", false, err
	}
	s.countPageView(ctx, p, pc.row.ID) // its tool parts start as on the page (pages_runs.go)
	viewer.Phone = s.viewerPhone(ctx, p)
	viewer.Personal = p.Can(PermTableRead)
	pc.values, _ = pc.page.FilterValues(nil, viewer, time.Now())

	var figures []string
	var list []string
	more := 0
	listed := false
	for i, b := range pc.page.Blocks {
		if _, off := pc.off[i]; off {
			continue
		}
		if _, broken := pc.broken[i]; broken {
			continue
		}
		switch {
		case b.Type == "figure":
			out := s.drawBlock(ctx, pc, b, pages.Cursor{}, nil)
			if shown, ok := out["shown"].(string); ok && out["unavailable"] == nil && out["error"] == nil {
				figures = append(figures, b.Label+" "+shown)
			}
		case (b.Type == "list" || b.Type == "table") && !listed:
			out := s.drawBlock(ctx, pc, b, pages.Cursor{}, nil)
			rows, ok := out["rows"].([]pages.Shown)
			if !ok || out["unavailable"] != nil || out["error"] != nil {
				continue
			}
			listed = true
			if b.Title != "" {
				list = append(list, "*"+b.Title+"*")
			}
			for j, r := range rows {
				if j == pageShortRows {
					break
				}
				list = append(list, "• "+shortRow(r))
			}
			n := len(rows)
			if c, ok := out["count"].(int64); ok && int(c) > n {
				n = int(c)
			} else if c, ok := out["count"].(int); ok && c > n {
				n = c
			}
			more = n - min(len(rows), pageShortRows)
			if len(rows) == 0 {
				list = append(list, nonEmpty(b.Empty, t("wa.tool.list_empty")))
			}
		}
	}
	var b strings.Builder
	b.WriteString("*" + pc.page.Title + "*")
	if len(figures) > 0 {
		b.WriteString("\n" + strings.Join(figures, " · "))
	}
	if len(list) > 0 {
		b.WriteString("\n\n" + strings.Join(list, "\n"))
	}
	if more > 0 {
		b.WriteString("\n" + t("wa.page.more", "count", fmt.Sprint(more)))
	}
	if s.PublicURL != "" {
		b.WriteString("\n\n" + t("wa.page.open", "link", strings.TrimRight(s.PublicURL, "/")+"/pages/"+url.PathEscape(pc.page.Name)))
	}
	return b.String(), true, nil
}

// shortRow is one row on a line: its title, its line and its state (a
// table's cells, joined).
func shortRow(r pages.Shown) string {
	if len(r.Cells) > 0 {
		var cells []string
		for _, c := range r.Cells {
			if c = strings.TrimSpace(c); c != "" {
				cells = append(cells, c)
			}
		}
		return strings.Join(cells, " · ")
	}
	line := nonEmpty(r.Title, "—")
	if r.Sub != "" {
		line += " — " + r.Sub
	}
	if r.State != nil && r.State.Text != "" {
		line += " (" + r.State.Text + ")"
	}
	return line
}
