package publicweb

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Rating a delivery from the status page (docs/spec/dispatch.md D5): once
// an order is delivered, its page asks how the delivery was, 1 to 5, for
// 14 days; one tap (POST /o/{reference}/rate?k=) keeps it against the
// delivery, never naming the rider to the customer. The business sees
// each rider's average, and is told when one falls below its mark.

// OrderDelivery is what an order's status page may show of its delivery.
type OrderDelivery struct {
	Status  string
	Rating  int
	CanRate bool
}

// OrderRate reads an order's delivery and keeps the customer's rating.
type OrderRate interface {
	// Delivery is ok false when the order has no delivery under way or
	// done.
	Delivery(ctx context.Context, reference string, statusHash []byte) (OrderDelivery, bool, error)
	// Rate is false when the delivery cannot be rated (not delivered,
	// rated already, or too long ago).
	Rate(ctx context.Context, reference string, statusHash []byte, rating int) (bool, error)
}

// DBOrderRate is OrderRate over the public role's functions.
type DBOrderRate struct{ Pool *pgxpool.Pool }

// Delivery implements OrderRate.
func (d DBOrderRate) Delivery(ctx context.Context, reference string, statusHash []byte) (OrderDelivery, bool, error) {
	var x OrderDelivery
	var rating *int
	err := d.Pool.QueryRow(ctx, `SELECT status, rating, can_rate FROM taskiem_public_order_delivery($1, $2)`, reference, statusHash).Scan(&x.Status, &rating, &x.CanRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrderDelivery{}, false, nil
	}
	if rating != nil {
		x.Rating = *rating
	}
	return x, err == nil, err
}

// Rate implements OrderRate.
func (d DBOrderRate) Rate(ctx context.Context, reference string, statusHash []byte, rating int) (bool, error) {
	var ok bool
	err := d.Pool.QueryRow(ctx, `SELECT taskiem_public_order_rate($1, $2, $3)`, reference, statusHash, rating).Scan(&ok)
	return ok, err
}

// rateView is the status page's How was the delivery?
type rateView struct {
	Action   string
	Given    int
	Stars    []int
	Business string
}

// rateDelivery adds the rating to a delivered order's page: the five
// buttons while it may be rated, the thanks once it is.
func (s *Server) rateDelivery(ctx context.Context, d *statusData, ref, key, prefix string) error {
	if s.OrderRate == nil {
		return nil
	}
	x, ok, err := s.OrderRate.Delivery(ctx, ref, storefront.StatusHash(key))
	if err != nil || !ok || x.Status != "delivered" {
		return err
	}
	switch {
	case x.Rating > 0:
		d.Rate = &rateView{Given: x.Rating}
	case x.CanRate:
		d.Rate = &rateView{Action: statusLink(prefix, ref, key, "rate"), Stars: []int{1, 2, 3, 4, 5}, Business: d.Business}
	}
	return nil
}

// serveOrderRate serves POST /o/{reference}/rate?k=: it keeps the rating
// and sends the customer back to the status page, which thanks them.
func (s *Server) serveOrderRate(w http.ResponseWriter, r *http.Request, site Site, ref string, embed bool) {
	if !allowed(w, r, http.MethodPost) {
		return
	}
	key := r.URL.Query().Get("k")
	if _, ok := s.statusOf(w, r, site, ref, key); !ok {
		return
	}
	ip := formguard.HashIP(s.ipHashKey(), s.ClientIP(r), s.now())
	if !s.Submissions.Allow(hex.EncodeToString(ip[:])) {
		w.Header().Set("Retry-After", "60")
		s.page(w, r, http.StatusTooManyRequests, "unavailable", pageData{Title: "Too many requests"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	n, err := strconv.Atoi(r.PostFormValue("rating"))
	if err != nil || n < 1 || n > 5 {
		s.page(w, r, http.StatusUnprocessableEntity, "notfound", pageData{Title: "Choose 1 to 5"})
		return
	}
	if s.OrderRate != nil {
		// Refused (rated already, too late): the page shows what it can.
		if _, err := s.OrderRate.Rate(r.Context(), ref, storefront.StatusHash(key), n); err != nil {
			s.tryLater(w, r, "order rating", err)
			return
		}
	}
	prefix := ""
	if embed {
		prefix = "/embed"
	}
	http.Redirect(w, r, statusLink(prefix, ref, key, ""), http.StatusSeeOther) //nolint:gosec // G710: a relative path built from a reference statusOf found
}
