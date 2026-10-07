package api

import (
	"errors"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/billing"
)

// Plans, subscriptions and billing (spec 16; docs/billing.md). With
// billing off (the default) every tenant is on the internal plan: these
// endpoints report it, plan features never refuse anything, and checkout
// and plan changes answer 409.

// PermBillingManage lets a member see invoices, pay, change plans and
// cancel. Owners hold it.
const PermBillingManage = "billing.manage"

func init() {
	allPermissions = append(allPermissions, PermBillingManage)
	rolePermissions["owner"] = allPermissions
	permissionInfo[PermBillingManage] = "Manage the subscription: plans, payments, invoices and cancellation"
}

// billingRoutes mounts /v1/billing inside the authenticated group.
func (s *Server) billingRoutes(r chi.Router) {
	r.Get("/status", s.billingStatus) // any member: the plan and banner
	manage := s.need(PermBillingManage)
	r.With(manage).Get("/", s.getBilling)
	r.With(manage).Get("/invoices/{id}", s.getInvoice)
	r.With(manage, s.tenantWide).Post("/checkout", s.billingCheckout)
	r.With(manage, s.tenantWide).Post("/plan", s.changePlan)
	r.With(manage, s.tenantWide).Post("/cancel", s.cancelSubscription)
}

func billingBy(r *http.Request) string {
	p := principalFrom(r.Context())
	return p.ActorType() + ":" + p.Actor()
}

// billingFail maps billing errors to responses.
func (s *Server) billingFail(w http.ResponseWriter, r *http.Request, err error) {
	var fe *billing.FeatureError
	var de *billing.DowngradeError
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": fe.Error(), "code": "plan_feature_required", "feature": fe.Feature})
	case errors.As(err, &de):
		writeJSON(w, http.StatusConflict, map[string]any{"error": de.Error(), "code": "downgrade_blocked", "blockers": de.Blockers})
	case errors.Is(err, billing.ErrInvalid):
		writeErr(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, billing.ErrConflict):
		writeErr(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "conflict: "))
	case errors.Is(err, billing.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	default:
		s.fail(w, r, err)
	}
}

// feature refuses a request when the tenant's plan lacks feature (402).
// With writesOnly, reads pass (a page can still show what is set up).
func (s *Server) feature(name string, writesOnly bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if writesOnly && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				next.ServeHTTP(w, r)
				return
			}
			if err := s.Billing.Require(r.Context(), principalFrom(r.Context()).TenantID, name); err != nil {
				s.billingFail(w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// partnerPlan gates the partner API on the embedded feature, and custom
// domains for embed apps on white-label.
func (s *Server) partnerPlan(next http.Handler) http.Handler {
	embedded, white := s.feature(billing.FeatureEmbedded, false), s.feature(billing.FeatureWhiteLabel, true)
	return embedded(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/domains") {
			white(next).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Server) billingStatus(w http.ResponseWriter, r *http.Request) {
	tenant := principalFrom(r.Context()).TenantID
	out := map[string]any{"enabled": s.Billing.On(), "plan": billing.InternalPlanID, "plan_name": billing.InternalPlan().Name}
	if s.Billing.On() {
		if err := s.Billing.Ensure(r.Context(), tenant, billingBy(r)); err != nil {
			s.billingFail(w, r, err)
			return
		}
		err := s.tx(r, func(tx pgx.Tx) error {
			p, sub, err := s.Billing.PlanOf(r.Context(), tx, tenant)
			if err != nil {
				return err
			}
			out["plan"], out["plan_name"], out["features"] = p.ID, p.Name, p.Features
			if sub != nil {
				out["status"] = sub.Status
				if b := s.Billing.Banner(sub); b != nil {
					out["banner"] = b
				}
			}
			return nil
		})
		if err != nil {
			s.billingFail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getBilling(w http.ResponseWriter, r *http.Request) {
	svc := s.Billing
	if svc == nil {
		svc = &billing.Service{Pool: s.Store.Pool, Store: s.Store} // billing off
	}
	ov, err := svc.Overview(r.Context(), principalFrom(r.Context()).TenantID)
	if err != nil {
		s.billingFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) getInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in billing.Invoice
	err = s.tx(r, func(tx pgx.Tx) error {
		var err error
		in, err = billing.InvoiceByID(r.Context(), tx, principalFrom(r.Context()).TenantID, id)
		return err
	})
	if err != nil {
		s.billingFail(w, r, err)
		return
	}
	if r.URL.Query().Get("format") == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = renderInvoice(w, in)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) billingCheckout(w http.ResponseWriter, r *http.Request) {
	var req billing.CheckoutRequest
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Billing.Checkout(r.Context(), principalFrom(r.Context()).TenantID, req, billingBy(r))
	if err != nil {
		s.billingFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type changePlanReq struct {
	Plan     string `json:"plan"`
	Interval string `json:"interval,omitempty"`
}

func (s *Server) changePlan(w http.ResponseWriter, r *http.Request) {
	var req changePlanReq
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Billing.ChangePlan(r.Context(), principalFrom(r.Context()).TenantID, req.Plan, req.Interval, billingBy(r))
	if err != nil {
		s.billingFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type cancelReq struct {
	// Resume undoes a cancellation before the period ends.
	Resume bool `json:"resume,omitempty"`
}

func (s *Server) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	var req cancelReq
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	sub, err := s.Billing.Cancel(r.Context(), principalFrom(r.Context()).TenantID, req.Resume, billingBy(r))
	if err != nil {
		s.billingFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscription": sub})
}

// billingWebhook receives a payment provider's events (unauthenticated:
// the provider's signature is the check). Bad signatures are 401; events
// already processed, and events not ours, are 200.
func (s *Server) billingWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	err = s.Billing.HandleWebhook(r.Context(), chi.URLParam(r, "provider"), r.Header, body)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case errors.Is(err, billing.ErrBadSignature):
		writeErr(w, http.StatusUnauthorized, "bad signature")
	case errors.Is(err, billing.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	default:
		s.Logger.Error("billing webhook", "provider", chi.URLParam(r, "provider"), "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error") // the provider retries
	}
}

var invoiceTmpl = template.Must(template.New("invoice").Funcs(template.FuncMap{
	"naira": billing.Naira,
	"day":   func(t time.Time) string { return t.Format("2 January 2006") },
	"pct":   func(bp int) string { return strconv.FormatFloat(float64(bp)/100, 'f', -1, 64) },
}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Invoice {{.Number}}</title>
<style>body{font:14px system-ui,sans-serif;max-width:720px;margin:2rem auto;color:#111}table{width:100%;border-collapse:collapse}
td,th{padding:6px 4px;border-bottom:1px solid #ddd;text-align:left}td.n,th.n{text-align:right}.muted{color:#666}</style></head><body>
<h1>Invoice {{.Number}}</h1>
<p class="muted">Issued {{day .IssuedAt}} · due {{day .DueAt}} · status <strong>{{.Status}}</strong>{{if .PaidAt}} · paid {{day .PaidAt}}{{end}}</p>
<p>Bill to: {{index .BillTo "name"}}{{with index .BillTo "email"}} ({{.}}){{end}}<br>Period: {{day .PeriodStart}} to {{day .PeriodEnd}}</p>
<table><tr><th>Description</th><th class="n">Qty</th><th class="n">Unit</th><th class="n">Amount</th></tr>
{{range .Lines}}<tr><td>{{.Description}}</td><td class="n">{{.Quantity}}</td><td class="n">{{naira .UnitKobo}}</td><td class="n">{{naira .AmountKobo}}</td></tr>{{end}}
<tr><td colspan="3" class="n">Subtotal</td><td class="n">{{naira .SubtotalKobo}}</td></tr>
<tr><td colspan="3" class="n">VAT ({{pct .VATRateBP}}%)</td><td class="n">{{naira .VATKobo}}</td></tr>
<tr><th colspan="3" class="n">Total ({{.Currency}})</th><th class="n">{{naira .TotalKobo}}</th></tr></table>
</body></html>`))

func renderInvoice(w io.Writer, in billing.Invoice) error { return invoiceTmpl.Execute(w, in) }
