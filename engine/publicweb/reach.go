package publicweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Updates on WhatsApp and Telegram for people who are not on Taskiem
// (no-technical-steps N11, decision 0039), on the form host:
//
//	a form's thank-you page   offers them when the workflow messages a phone typed in it
//	GET /u/{public_id}         a business's standing page
//	GET /u/{public_id}/{app}   makes a code and goes to the bot (telegram) or number (whatsapp)
//
// A link carries a one-time code; the api role, which receives the bot's
// and number's messages, links the person who sends it back.

// ReachStore is what the form host does for updates.
type ReachStore interface {
	// Targets are where a business's links point: the bot's username and
	// a WhatsApp number (+E.164); "" when that app is off.
	Targets(ctx context.Context, tenant uuid.UUID) (telegram, whatsapp string, err error)
	// Code keeps a code for a business (a submission's, or none); false
	// when it takes no more now.
	Code(ctx context.Context, codeHash []byte, tenant uuid.UUID, reference string, payload []byte) (bool, error)
	// Page is a standing page's business.
	Page(ctx context.Context, publicID string) (tenant uuid.UUID, business string, ok bool, err error)
}

// DBReach is the ReachStore on a pool connected as taskiem_public.
func DBReach(pool *pgxpool.Pool) ReachStore { return dbReach{pool} }

type dbReach struct{ pool *pgxpool.Pool }

func (d dbReach) Targets(ctx context.Context, tenant uuid.UUID) (string, string, error) {
	var tg, wa *string
	if err := d.pool.QueryRow(ctx, `SELECT telegram, whatsapp FROM taskiem_reach_targets($1)`, tenant).Scan(&tg, &wa); err != nil {
		return "", "", err
	}
	var a, b string
	if tg != nil {
		a = *tg
	}
	if wa != nil {
		b = *wa
	}
	return a, b, nil
}

func (d dbReach) Code(ctx context.Context, codeHash []byte, tenant uuid.UUID, reference string, payload []byte) (bool, error) {
	var ref *string
	if reference != "" {
		ref = &reference
	}
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT taskiem_reach_code_put($1, $2, $3, $4)`, codeHash, tenant, ref, payload).Scan(&ok)
	return ok, err
}

func (d dbReach) Page(ctx context.Context, publicID string) (uuid.UUID, string, bool, error) {
	var t uuid.UUID
	var name string
	err := d.pool.QueryRow(ctx, `SELECT tenant_id, business FROM taskiem_reach_page($1)`, publicID).Scan(&t, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", false, nil
	}
	return t, name, err == nil, err
}

// updateLink is one app's button.
type updateLink struct {
	App   string // telegram or whatsapp
	Label string
	URL   string
}

// reachData is the standing page's input.
type reachData struct {
	pageData
	Business string
	Links    []updateLink
	Home     string
}

// TelegramLink and WhatsAppLink are where a code is sent from: the bot
// opened with the code, or a chat with the number holding a message with
// it, which the person sends.
func TelegramLink(bot, code string) string {
	return "https://t.me/" + url.PathEscape(bot) + "?start=u_" + code
}

func WhatsAppLink(number, business, code string) string {
	return "https://wa.me/" + strings.TrimPrefix(number, "+") + "?text=" + url.QueryEscape(WhatsAppText(business, code))
}

// WhatsAppText is the message a WhatsApp link fills in.
func WhatsAppText(business, code string) string {
	return "Send me updates from " + sender.CleanName(business) + " here. Code U-" + code
}

// updateLinks makes a code for each app the business can be reached on
// and its button. phones are those typed in the submission (reference),
// proven the one its form verified. Nothing when updates are off.
func (s *Server) updateLinks(ctx context.Context, tenant uuid.UUID, business, reference string, phones []string, proven string) []updateLink {
	if s.Reach == nil || s.FormKeys == nil {
		return nil
	}
	tg, wa, err := s.Reach.Targets(ctx, tenant)
	if err != nil {
		s.Logger.Warn("form host: where updates go", "err", err)
		return nil
	}
	var out []updateLink
	for _, app := range [][2]string{{reach.WhatsApp, wa}, {reach.Telegram, tg}} {
		if app[1] == "" {
			continue
		}
		code, hash, err := reach.NewCode()
		if err != nil {
			return out
		}
		var payload []byte
		if reference != "" {
			if payload, err = s.FormKeys.Seal(reach.PayloadAAD(hash), reach.Payload{Phones: phones, Proven: proven}); err != nil {
				return out
			}
		}
		ok, err := s.Reach.Code(ctx, hash, tenant, reference, payload)
		if err != nil || !ok {
			if err != nil {
				s.Logger.Warn("form host: making an update link", "err", err)
			}
			return out
		}
		if app[0] == reach.Telegram {
			out = append(out, updateLink{App: app[0], Label: "Get updates on Telegram", URL: TelegramLink(app[1], code)})
		} else {
			out = append(out, updateLink{App: app[0], Label: "Get updates on WhatsApp", URL: WhatsAppLink(app[1], business, code)})
		}
	}
	return out
}

// reachPhones are the phones typed in fields (ingest.ReachFields), as
// typed (the api role reads them), and the one the form verified, which is
// among them when "$respondent" is a field.
func (hf *hform) reachPhones(st *hstate, answers map[string]any) (phones []string, proven string) {
	if a := st.Verified; a != nil && a.Channel == "sms" {
		proven = a.Address
	}
	for _, f := range hf.pub.Reach {
		v, _ := answers[f].(string)
		if f == "$respondent" {
			v = proven
		}
		if v = strings.TrimSpace(v); v != "" && len(v) <= 32 {
			phones = append(phones, v)
		}
	}
	return phones, proven
}

var reachAppRe = regexp.MustCompile(`^(telegram|whatsapp)$`)

// serveReach is a business's standing page, /u/{public_id}, and its
// buttons, /u/{public_id}/{app}: a code is made only when one is pressed.
func (s *Server) serveReach(w http.ResponseWriter, r *http.Request, id, app string) {
	if !allowed(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if s.Reach == nil {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	tenant, business, ok, err := s.Reach.Page(r.Context(), id)
	if err != nil {
		s.tryLater(w, r, "updates page", err)
		return
	}
	if !ok {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	tg, wa, err := s.Reach.Targets(r.Context(), tenant)
	if err != nil {
		s.tryLater(w, r, "updates page", err)
		return
	}
	if app == "" {
		d := reachData{pageData: pageData{Title: "Get updates", Lang: "en", Look: s.lookFor(r.Context(), tenant, business)}, Business: business}
		if wa != "" {
			d.Links = append(d.Links, updateLink{App: reach.WhatsApp, Label: "Get updates on WhatsApp", URL: "/u/" + id + "/whatsapp"})
		}
		if tg != "" {
			d.Links = append(d.Links, updateLink{App: reach.Telegram, Label: "Get updates on Telegram", URL: "/u/" + id + "/telegram"})
		}
		s.renderPrivate(w, r, http.StatusOK, "reach.html", d)
		return
	}
	target := map[string]string{reach.Telegram: tg, reach.WhatsApp: wa}[app]
	if target == "" {
		s.page(w, r, http.StatusNotFound, "notfound", pageData{Title: "Page not found"})
		return
	}
	if !s.formLimiter().Allow(s.ipKey(r)) {
		w.Header().Set("Retry-After", "60")
		s.formNotice(w, r, http.StatusTooManyRequests, "Please wait", tenant, business, "Too many links were asked for from this connection. Please wait a few minutes.")
		return
	}
	code, hash, err := reach.NewCode()
	if err == nil {
		if ok, err = s.Reach.Code(r.Context(), hash, tenant, "", nil); err == nil && !ok {
			err = errors.New("no more codes now")
		}
	}
	if err != nil {
		s.tryLater(w, r, "update link", err)
		return
	}
	link := TelegramLink(target, code)
	if app == reach.WhatsApp {
		link = WhatsAppLink(target, business, code)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", link)
	w.WriteHeader(http.StatusSeeOther)
}
