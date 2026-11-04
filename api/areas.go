package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/areas"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/templates"
)

// Business areas (docs/spec/business-areas.md): which parts of Taskiem a
// business uses. An area that is off is refused here, centrally, for every
// route under its paths, so a route added later cannot be forgotten; the
// web app also hides it. Nothing is deleted when an area goes off.

// areaPaths say which area each route family belongs to. The longest
// matching prefix wins, so /v1/storefront/orders is Orders although
// /v1/storefront is the shop. The Schools area is refused by the schools
// package itself, and everything not listed is always on.
var areaPaths = []struct {
	prefix string
	area   areas.Area
}{
	{"/v1/storefront/orders", areas.Orders},
	{"/v1/trade-orders", areas.Orders},

	{"/v1/storefront", areas.Storefront},
	{"/v1/riders", areas.Storefront},
	{"/v1/deliveries", areas.Storefront},
	{"/v1/dispatch", areas.Storefront},

	{"/v1/stock", areas.Stock},
	{"/v1/price-lists", areas.Stock},
	{"/v1/link-prices", areas.Stock},
	{"/v1/supplier-prices", areas.Stock},
	{"/v1/item-links", areas.Stock},

	{"/v1/invoices", areas.Money},
	{"/v1/invoice-settings", areas.Money},
	{"/v1/money", areas.Money},
	{"/v1/customer-accounts", areas.Money},
	{"/v1/bills", areas.Money},

	{"/v1/links", areas.Links},
	{"/v1/link-settings", areas.Links},
	{"/v1/link-chats", areas.Links},
}

// areaOfPath names the area a request path belongs to, if any.
func areaOfPath(path string) (areas.Area, bool) {
	path = strings.TrimSuffix(path, "/")
	best, found := "", areas.Area("")
	for _, x := range areaPaths {
		if (path == x.prefix || strings.HasPrefix(path, x.prefix+"/")) && len(x.prefix) > len(best) {
			best, found = x.prefix, x.area
		}
	}
	return found, best != ""
}

// areaGate refuses a request to a route of an area the business has turned
// off, with 404 (as for a part that is not there) and a plain sentence. It
// reports whether the request may go on.
func (s *Server) areaGate(w http.ResponseWriter, r *http.Request, p *Principal) bool {
	area, ok := areaOfPath(r.URL.Path)
	if !ok || p.TenantID == uuid.Nil {
		return true
	}
	var on bool
	if err := db.InTenantTx(r.Context(), s.Store.Pool, []uuid.UUID{p.TenantID}, func(tx pgx.Tx) (err error) {
		on, err = areas.Enabled(r.Context(), tx, p.TenantID, area)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return false
	}
	if !on {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":    areaOffSentence(area),
			"area_off": string(area),
		})
		return false
	}
	return true
}

func areaOffSentence(a areas.Area) string {
	for _, i := range areas.Catalogue() {
		if i.Area == a {
			return i.Name + " is not turned on for this business. An owner turns it on in Settings."
		}
	}
	return "This part of Taskiem is not turned on for this business."
}

type areaRow struct {
	areas.Info
	Enabled bool `json:"enabled"`
}

func areaRows(st areas.State) []areaRow {
	out := []areaRow{}
	for _, i := range areas.Catalogue() {
		out = append(out, areaRow{Info: i, Enabled: st.On(i.Area)})
	}
	return out
}

// getAreas answers GET /v1/settings/areas: every area, in plain words,
// with whether it is on and what it needs. Any member may read it.
func (s *Server) getAreas(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var st areas.State
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		st, err = areas.Load(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"areas": areaRows(st)})
}

// putAreas answers PUT /v1/settings/areas with {"areas": {"stock": true,
// "money": false}, "confirm": false}. Only an owner changes areas. Turning
// an area on turns on what it needs; turning one off is refused while
// another area that needs it stays on (409). When published workflows use
// an area being turned off, the answer is 409 with "affected" and nothing
// changes until the request is sent again with confirm. Off hides and
// refuses; nothing is deleted. Audited.
func (s *Server) putAreas(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if !slices.Contains(p.Roles, "owner") {
		writeErr(w, http.StatusForbidden, "only an owner turns parts of Taskiem on or off")
		return
	}
	var req struct {
		Areas   map[string]bool `json:"areas"`
		Confirm bool            `json:"confirm"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	want := map[areas.Area]bool{}
	for name, on := range req.Areas {
		a, ok := areas.Valid(name)
		if !ok {
			s.fail(w, r, fmt.Errorf("%w: %q is not one of storefront, orders, stock, money, links, schools", errBadRequest, name))
			return
		}
		want[a] = on
	}
	if len(want) == 0 {
		s.fail(w, r, fmt.Errorf("%w: say which areas to turn on or off", errBadRequest))
		return
	}
	var affected []areas.UsedBy
	var after areas.State
	err := s.tx(r, func(tx pgx.Tx) error {
		ctx := r.Context()
		if err := lockAreas(ctx, tx, p.TenantID); err != nil {
			return err
		}
		before, err := areas.Load(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if after, err = areas.Apply(before, want); err != nil {
			if errors.As(err, new(*areas.BlockedError)) {
				return fmt.Errorf("%w: %s", errConflict, err.Error())
			}
			return err
		}
		changes := map[string]bool{}
		for _, a := range areas.All {
			if before.On(a) == after.On(a) {
				continue
			}
			changes[string(a)] = after.On(a)
			if after.On(a) {
				continue
			}
			used, err := areas.Affected(ctx, tx, p.TenantID, a)
			if err != nil {
				return err
			}
			affected = append(affected, used...)
		}
		if len(affected) > 0 && !req.Confirm {
			return nil
		}
		if len(changes) == 0 {
			return nil
		}
		if _, err := areas.Save(ctx, tx, p.TenantID, before, after, p.Actor()); err != nil {
			return err
		}
		// The public page follows the shop: closed while it is off, open
		// again when it comes back (if the area was what closed it).
		if before.On(areas.Storefront) != after.On(areas.Storefront) {
			var err error
			if after.On(areas.Storefront) {
				err = storefront.ReopenForArea(ctx, tx, p.TenantID)
			} else {
				err = storefront.CloseForArea(ctx, tx, p.TenantID)
			}
			if err != nil {
				return err
			}
		}
		if before.On(areas.Schools) != after.On(areas.Schools) {
			if _, err := schools.SetEnabled(ctx, tx, p.TenantID, after.On(areas.Schools), p.Actor()); err != nil {
				return err
			}
			if err := auditTx(r, tx, "settings.schools", p.TenantID.String(), map[string]any{"enabled": after.On(areas.Schools)}); err != nil {
				return err
			}
		}
		return auditTx(r, tx, "settings.areas", p.TenantID.String(), map[string]any{"changes": changes})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(affected) > 0 && !req.Confirm {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    "Workflows of yours use a part you are turning off. Send the request again with confirm to turn it off anyway.",
			"affected": affected,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"areas": areaRows(after)})
}

// lockAreas serialises changes to one business's areas, so two requests
// cannot each pass the dependency check and together leave an area on
// without what it needs.
func lockAreas(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('business_areas:' || $1::text, 0))`, tenant.String())
	return err
}

// schoolsNeedMoney keeps the rule that Schools needs Money when the Schools
// switch is turned on through its own endpoint (PUT /v1/settings/schools)
// rather than through the areas API: Money is turned on with it.
func schoolsNeedMoney(r *http.Request, tx pgx.Tx, p *Principal) error {
	ctx := r.Context()
	if err := lockAreas(ctx, tx, p.TenantID); err != nil {
		return err
	}
	before, err := areas.Load(ctx, tx, p.TenantID)
	if err != nil {
		return err
	}
	after, err := areas.Apply(before, map[areas.Area]bool{areas.Schools: true})
	if err != nil {
		return err
	}
	changed, err := areas.Save(ctx, tx, p.TenantID, before, after, p.Actor())
	if err != nil || len(changed) == 0 {
		return err
	}
	return auditTx(r, tx, "settings.areas", p.TenantID.String(), map[string]any{"changes": map[string]bool{string(areas.Money): true}, "because": "schools"})
}

// chatAreas says which parts of Taskiem the business behind a chat uses,
// read once for a message. A read that fails shows everything: the
// handlers behind it still refuse what is off.
func (s *Server) chatAreas(ctx context.Context, c *chat) areas.State {
	if c.areaState != nil {
		return c.areaState
	}
	st := areas.State{}
	for _, a := range areas.All {
		st[a] = true
	}
	if c.tenant.ID != uuid.Nil {
		var loaded areas.State
		if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{c.tenant.ID}, func(tx pgx.Tx) (err error) {
			loaded, err = areas.Load(ctx, tx, c.tenant.ID)
			return err
		}); err == nil {
			st = loaded
		}
	}
	c.areaState = st
	return st
}

// chatIntentOn reports whether the part of Taskiem a command in the
// person's language belongs to is on.
func chatIntentOn(on areas.State, in lang.Intent) bool {
	switch in {
	case lang.IntentInvoice, lang.IntentExpense, lang.IntentCashIn, lang.IntentCashOut, lang.IntentOwes, lang.IntentPaid:
		return on.On(areas.Money)
	case lang.IntentMakePage, lang.IntentMyPage, lang.IntentOpenShop, lang.IntentCloseShop, lang.IntentQR, lang.IntentReady, lang.IntentDelivered,
		lang.IntentFronts, lang.IntentAddFront, lang.IntentFrontOn, lang.IntentFrontOff:
		return on.On(areas.Storefront)
	case lang.IntentOrders:
		return on.On(areas.Orders)
	case lang.IntentSold, lang.IntentCount, lang.IntentStock, lang.IntentTakeOff, lang.IntentReceived:
		return on.On(areas.Stock)
	}
	return true
}

// templatesOfAreasOn keeps the templates whose connectors and trigger belong
// only to parts of Taskiem the business has on. A read that fails offers all.
func (s *Server) templatesOfAreasOn(ctx context.Context, tenant uuid.UUID, list []*templates.Template) []*templates.Template {
	var on areas.State
	if err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) (err error) {
		on, err = areas.Load(ctx, tx, tenant)
		return err
	}); err != nil {
		return list
	}
	kept := make([]*templates.Template, 0, len(list))
	for _, t := range list {
		ok := true
		for _, c := range t.Connectors {
			if a, has := areas.ConnectorArea(c); has && !on.On(a) {
				ok = false
			}
		}
		var head struct {
			Trigger struct {
				Type string `json:"type"`
			} `json:"trigger"`
		}
		if json.Unmarshal(t.Definition, &head) == nil {
			if a, has := areas.TriggerArea(head.Trigger.Type); has && !on.On(a) {
				ok = false
			}
		}
		if ok {
			kept = append(kept, t)
		}
	}
	return kept
}
