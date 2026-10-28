package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/datatables"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/storefront"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Dispatch (docs/dispatch.md D2 to D4): an order offered to a business's
// rider in their chat with signed buttons, the rider's taps and words,
// their photos and locations at the door, and the owner's view and hand.

// --- the rider's buttons ---

// deliveryButtonPrefix marks a rider's button: an action on a delivery,
// signed for the business, the delivery and the rider's contact.
const deliveryButtonPrefix = "dl1."

var deliveryActions = map[byte]string{'a': dispatch.Accept, 'c': dispatch.Cant, 'p': dispatch.PickedUp, 'r': dispatch.Arrived, 'd': dispatch.Delivered,
	'm': dispatch.Cash, 'n': dispatch.NewNumber}

type deliveryButton struct {
	Action   byte
	Tenant   uuid.UUID
	Delivery uuid.UUID
	Contact  uuid.UUID
}

var errBadDeliveryButton = errors.New("api: not a valid delivery button")

func (b deliveryButton) body() []byte {
	out := make([]byte, 0, 49)
	out = append(out, b.Action)
	out = append(out, b.Tenant[:]...)
	out = append(out, b.Delivery[:]...)
	return append(out, b.Contact[:]...)
}

func deliveryButtonMAC(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("taskiem/delivery-button/v1\x00"))
	m.Write(body)
	return m.Sum(nil)[:16]
}

func signDeliveryButton(key []byte, b deliveryButton) string {
	body := b.body()
	return deliveryButtonPrefix + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(deliveryButtonMAC(key, body))
}

func parseDeliveryButton(key []byte, payload string) (deliveryButton, error) {
	var b deliveryButton
	rest, ok := strings.CutPrefix(payload, deliveryButtonPrefix)
	if !ok || len(key) < 32 || len(payload) > 200 {
		return b, errBadDeliveryButton
	}
	b64, m64, ok := strings.Cut(rest, ".")
	if !ok {
		return b, errBadDeliveryButton
	}
	body, err1 := base64.RawURLEncoding.DecodeString(b64)
	mac, err2 := base64.RawURLEncoding.DecodeString(m64)
	if err1 != nil || err2 != nil || len(body) != 49 || len(mac) != 16 || !hmac.Equal(mac, deliveryButtonMAC(key, body)) {
		return b, errBadDeliveryButton
	}
	if _, ok := deliveryActions[body[0]]; !ok {
		return b, errBadDeliveryButton
	}
	b.Action = body[0]
	copy(b.Tenant[:], body[1:17])
	copy(b.Delivery[:], body[17:33])
	copy(b.Contact[:], body[33:49])
	return b, nil
}

// buttonLabels are the message ids of the actions' button titles.
var buttonLabels = map[byte]string{'a': "rider.btn.accept", 'c': "rider.btn.cant", 'p': "rider.btn.picked_up", 'r': "rider.btn.arrived",
	'd': "rider.btn.delivered", 'm': "rider.btn.cash", 'n': "rider.btn.new_number"}

func deliveryButtons(l lang.Tag, key []byte, b deliveryButton, actions ...byte) []whatsapp.Button {
	out := make([]whatsapp.Button, 0, len(actions))
	for _, a := range actions {
		b.Action = a
		out = append(out, whatsapp.Button{ID: signDeliveryButton(key, b), Title: tr(l, buttonLabels[a])})
	}
	return out
}

// --- what a rider is sent ---

// riderMessage is a stage's text and buttons for a rider.
func riderMessage(l lang.Tag, v dispatch.View, stage string) (string, []byte) {
	fee := ""
	if v.FeeKobo > 0 {
		fee = "\n" + tr(l, "rider.fee", "fee", storefront.FormatNaira(v.FeeKobo))
	}
	drop := v.Area
	if v.Held() && v.Address != "" {
		drop = v.Address
		if v.Area != "" {
			drop += " (" + v.Area + ")"
		}
	}
	var lines []string
	add := func(id string, kv ...string) { lines = append(lines, tr(l, id, kv...)) }
	where := func() {
		if v.Pickup != "" {
			add("rider.pickup", "pickup", strings.TrimSpace(v.Pickup+" "+v.PickupLink))
		}
		if drop != "" {
			add("rider.drop", "drop", drop)
		}
		if v.Landmark != "" {
			add("rider.landmark", "landmark", v.Landmark)
		}
		if v.Pin != "" {
			add("rider.pin", "pin", v.Pin)
		}
	}
	due := ""
	if v.DueKobo > 0 {
		due = storefront.FormatNaira(v.DueKobo)
	}
	switch stage {
	case dispatch.StageOffer:
		add("rider.offer", "reference", v.Reference)
		where()
		add("rider.items", "items", v.Items)
		if due != "" {
			add("rider.collect", "total", due)
		}
		return strings.Join(lines, "\n") + fee + "\n" + tr(l, "rider.offer_tail", "time", riderClock(v.ExpiresAt)), []byte{'a', 'c'}
	case dispatch.StageHeld:
		add("rider.held", "reference", v.Reference)
		add("rider.customer", "customer", v.Customer, "phone", v.CustomerPhone)
		where()
		add("rider.items", "items", v.Items)
		if due != "" {
			add("rider.collect_door", "total", due)
		}
		return strings.Join(lines, "\n") + fee + "\n" + tr(l, "rider.held_tail"), []byte{'p'}
	case dispatch.StageOnWay:
		return tr(l, "rider.on_way", "reference", v.Reference), []byte{'r'}
	case dispatch.StageArrived:
		return tr(l, "rider.arrived", "reference", v.Reference), []byte{'d'}
	case dispatch.StageDoor:
		if v.Door == nil {
			return tr(l, "rider.unpaid", "reference", v.Reference, "total", due), []byte{'m', 'n'}
		}
		return tr(l, "rider.door", "total", storefront.FormatNaira(v.Door.AmountKobo), "account", v.Door.Number, "bank", v.Door.Bank, "name", v.Door.Name,
			"time", riderClock(v.Door.ExpiresAt)), []byte{'m', 'n'}
	case dispatch.StageUnpaid:
		return tr(l, "rider.unpaid", "reference", v.Reference, "total", due), []byte{'m', 'n'}
	case dispatch.StagePaid:
		return tr(l, "rider.paid", "reference", v.Reference), []byte{'d'}
	case dispatch.StageDone:
		if v.Proof {
			return tr(l, "rider.done_proof", "reference", v.Reference), nil
		}
		return tr(l, "rider.done", "reference", v.Reference), nil
	case dispatch.StageCant:
		return tr(l, "rider.cant", "reference", v.Reference), nil
	case dispatch.StageReleased:
		return tr(l, "rider.released", "reference", v.Reference, "business", v.Business), nil
	case dispatch.StageWithdrawn:
		return tr(l, "rider.withdrawn", "reference", v.Reference), nil
	}
	return "", nil
}

func riderClock(t time.Time) string { return t.In(lagosTZ).Format("15:04") }

var lagosTZ = func() *time.Location {
	l, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		return time.FixedZone("WAT", 3600)
	}
	return l
}()

// stageLive reports whether a stage still fits the delivery: an offer
// that passed, or a tap the rider moved past, is not sent.
func stageLive(stage, status string) bool {
	switch stage {
	case dispatch.StageOffer:
		return status == "offered"
	case dispatch.StageHeld:
		return status == "accepted"
	case dispatch.StageOnWay:
		return status == "picked_up"
	case dispatch.StageArrived, dispatch.StageDoor, dispatch.StageUnpaid, dispatch.StagePaid:
		return status == "arrived" || status == "picked_up"
	}
	return true
}

// sendRiderItem sends a queued message to a rider on the chat app they
// tapped in on, with its buttons; outside WhatsApp's window an offer goes
// as its template (Accept and Can't as quick replies), anything else as
// an update the rider answers in words.
func (s *Server) sendRiderItem(ctx context.Context, it whatsapp.OutboxItem) error {
	key := s.chatSigner().Key
	if !s.Dispatch.Ready() || !s.Reach.Ready() || len(key) < 32 {
		return fmt.Errorf("%w: dispatch is off", whatsapp.ErrNothingToSend)
	}
	if it.Contact == nil || it.Run == nil || it.Step == nil {
		return fmt.Errorf("%w: incomplete", whatsapp.ErrNothingToSend)
	}
	ch, err := s.chatChannelFor(ctx, it.Channel, it.Tenant)
	if err != nil {
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	stage := *it.Step
	var v dispatch.View
	var addr string
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{it.Tenant}, func(tx pgx.Tx) error {
		var err error
		if v, err = s.Dispatch.ViewTx(ctx, tx, it.Tenant, *it.Run); err != nil {
			return err
		}
		addr, err = s.Reach.AddressTx(ctx, tx, it.Tenant, reach.Target{Channel: it.Channel, Contact: *it.Contact})
		return err
	})
	switch {
	case errors.Is(err, reach.ErrGone), errors.Is(err, dispatch.ErrNoDelivery):
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	case err != nil:
		return err
	}
	if !stageLive(stage, v.Status) {
		return fmt.Errorf("%w: the delivery moved on", whatsapp.ErrNothingToSend)
	}
	l := s.chooseLang(ctx, it.Tenant, addr, "").Tag
	text, actions := riderMessage(l, v, stage)
	if text == "" {
		return fmt.Errorf("%w: no stage %q", whatsapp.ErrNothingToSend, stage)
	}
	b := deliveryButton{Tenant: it.Tenant, Delivery: *it.Run, Contact: *it.Contact}
	buttons := deliveryButtons(l, key, b, actions...)
	m := whatsapp.Message{Tenant: it.Tenant, Buttons: buttons, Text: ch.Prefix(v.Business) + text}
	if stage == dispatch.StageOffer {
		tpl := whatsapp.TplDeliveryOffer
		summary := v.Reference
		if v.Area != "" {
			summary += " to " + v.Area
		}
		summary += ", " + v.Items
		if v.FeeKobo > 0 {
			summary += ", your fee " + storefront.FormatNaira(v.FeeKobo)
		}
		m.Template, m.Vars = &tpl, map[string]string{"tenant": v.Business, "summary": oneLine(summary)}
		m.Payloads = []string{buttons[0].ID, buttons[1].ID}
	} else {
		tpl := whatsapp.TplUpdate
		m.Template, m.Vars = &tpl, map[string]string{"tenant": v.Business, "about": "delivery " + v.Reference, "message": oneLine(text)}
	}
	_, err = ch.Send(ctx, addr, m)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsapp.ErrWindowClosed), whatsapp.TemplateUnusable(err):
		return fmt.Errorf("%w: outside WhatsApp's 24-hour window: %w", whatsapp.ErrNothingToSend, err)
	case errors.Is(err, telegram.ErrBlocked):
		if berr := s.Reach.Blocked(ctx, it.Channel, addr); berr != nil {
			return berr
		}
		return fmt.Errorf("%w: %w", whatsapp.ErrNothingToSend, err)
	}
	return fmt.Errorf("to %s: %w", whatsapp.MaskNumber(addr), err)
}

// oneLine fits text in a template's variable: no new lines, at most 900
// characters.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 900 {
		s = string(r[:900])
	}
	return s
}

// riderPress takes a tapped rider's button from any chat address, member
// or not; false when the payload is not a rider's button.
func (s *Server) riderPress(ctx context.Context, ch chatChannel, channel, addr, payload string) (bool, error) {
	if !strings.HasPrefix(payload, deliveryButtonPrefix) {
		return false, nil
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	invalid := func() (bool, error) { return true, s.waSay(ctx, ch, addr, "", tr(l, "rider.button_invalid")) }
	if !s.Dispatch.Ready() || !s.Reach.Ready() {
		return invalid()
	}
	if !s.limiter("rider-press:"+addr, time.Minute, 12).Allow() {
		return true, nil
	}
	b, err := parseDeliveryButton(s.chatSigner().Key, payload)
	if err != nil || (ch.OwnTenant() != uuid.Nil && ch.OwnTenant() != b.Tenant) {
		return invalid()
	}
	// The pressing address must be the contact the button was sent to,
	// and that contact the delivery's rider.
	var mine bool
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{b.Tenant}, func(tx pgx.Tx) error {
		h, found, err := s.Reach.ContactAtTx(ctx, tx, b.Tenant, b.Contact, channel, addr)
		if err != nil || !found {
			return err
		}
		rh, err := s.Dispatch.RiderPhoneHashTx(ctx, tx, b.Tenant, b.Delivery)
		if errors.Is(err, dispatch.ErrNoDelivery) {
			return nil
		}
		mine = err == nil && hmac.Equal(h, rh)
		return err
	})
	if err != nil {
		return true, err
	}
	if !mine {
		return invalid()
	}
	return true, s.riderAct(ctx, ch, addr, b.Tenant, b.Delivery, deliveryActions[b.Action])
}

// riderAct takes a rider's action on a delivery and answers a refusal;
// what follows a tap comes as the next message (sendRiderItem).
func (s *Server) riderAct(ctx context.Context, ch chatChannel, addr string, tenant, delivery uuid.UUID, action string) error {
	_, err := s.Dispatch.Press(ctx, tenant, delivery, action)
	l := s.chooseLang(ctx, tenant, addr, "").Tag
	switch {
	case errors.Is(err, dispatch.ErrMove), errors.Is(err, storefront.ErrDecision):
		msg := err.Error()
		for _, p := range []string{"dispatch: that does not fit the delivery now: ", "storefront: "} {
			msg = strings.ReplaceAll(msg, p, "")
		}
		return s.waSay(ctx, ch, addr, "", tr(l, "rider.refused", "reason", msg))
	case errors.Is(err, dispatch.ErrNoDelivery):
		return s.waSay(ctx, ch, addr, "", tr(l, "rider.button_invalid"))
	case err != nil:
		return err
	}
	// The next message goes at once, not at the next tick.
	if err := s.waDrainOutbox(ctx, tenant); err != nil {
		s.Logger.Warn("dispatch: sending the next message", "err", err)
	}
	return nil
}

// --- the rider's words, photos and locations ---

// riderActionWords are the words a rider may send for a tap.
var riderActionWords = map[string]string{
	"accept": dispatch.Accept, "take": dispatch.Accept, "i'll take it": dispatch.Accept,
	"can't": dispatch.Cant, "cant": dispatch.Cant, "pass": dispatch.Cant,
	"picked up": dispatch.PickedUp, "picked": dispatch.PickedUp, "pickup": dispatch.PickedUp,
	"arrived": dispatch.Arrived, "here": dispatch.Arrived, "at the door": dispatch.Arrived,
	"delivered": dispatch.Delivered, "done": dispatch.Delivered,
	"cash": dispatch.Cash, "cash received": dispatch.Cash,
	"new number": dispatch.NewNumber,
}

// actionFits names the statuses a delivery must have for an action.
var actionFits = map[string][]string{
	dispatch.Accept: {"offered"}, dispatch.Cant: {"offered"}, dispatch.PickedUp: {"accepted"}, dispatch.Arrived: {"picked_up", "accepted"},
	dispatch.Delivered: {"arrived", "picked_up"}, dispatch.Cash: {"arrived", "picked_up"}, dispatch.NewNumber: {"arrived"},
}

// riderDeliveryWord takes a rider's word for a tap ("picked up") on the
// delivery it fits, from an address linked to no member; false when the
// words are none of those, or the sender has no delivery they fit.
func (s *Server) riderDeliveryWord(ctx context.Context, ch chatChannel, channel, addr, text string) (bool, error) {
	if !s.Dispatch.Ready() {
		return false, nil
	}
	w := strings.Join(strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(text), "/ .!"))), " ")
	action, ok := riderActionWords[w]
	if !ok {
		return false, nil
	}
	phone := ""
	if channel == reach.WhatsApp {
		phone = addr
	}
	list, err := s.Dispatch.RiderDeliveries(ctx, phone, channel, addr)
	if err != nil || len(list) == 0 {
		return false, err
	}
	for _, o := range list {
		for _, st := range actionFits[action] {
			if o.Status == st && (ch.OwnTenant() == uuid.Nil || ch.OwnTenant() == o.Tenant) {
				return true, s.riderAct(ctx, ch, addr, o.Tenant, o.Delivery, action)
			}
		}
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	return true, s.waSay(ctx, ch, addr, "", tr(l, "rider.no_delivery"))
}

// riderMedia keeps a rider's photo (proof of delivery) or location (at
// the door) on their delivery: the one at the door, or delivered in the
// last two hours. false when the sender has none.
func (s *Server) riderMedia(ctx context.Context, c *chat, channel string) (bool, error) {
	in := c.in
	if !s.Dispatch.Ready() || (in.Image == nil && in.Location == "") {
		return false, nil
	}
	phone := ""
	if channel == reach.WhatsApp {
		phone = c.addr
	}
	list, err := s.Dispatch.RiderDeliveries(ctx, phone, channel, c.addr)
	if err != nil {
		return false, err
	}
	var target *dispatch.Open
	for i, o := range list {
		if o.Status == "arrived" || o.Status == "delivered" || o.Status == "picked_up" {
			target = &list[i]
			break
		}
	}
	if target == nil {
		return false, nil
	}
	l := s.chooseLang(ctx, c.ch.OwnTenant(), c.addr, "").Tag
	if in.Location != "" {
		if err := s.Dispatch.SetProof(ctx, target.Tenant, target.Delivery, "", in.Location); err != nil {
			return true, err
		}
		return true, s.waSay(ctx, c.ch, c.addr, "", tr(l, "rider.location_kept"))
	}
	if !s.limiter("rider-photo:"+c.addr, time.Minute, 4).Allow() {
		return true, nil
	}
	data, _, _, err := s.fetchMedia(ctx, c, in.Image, media.MaxImageBytes)
	if err != nil {
		s.Logger.Warn("dispatch: fetching a rider's photo", "err", err)
		return true, s.waSay(ctx, c.ch, c.addr, "", tr(l, "rider.photo_failed"))
	}
	path, err := s.storeProof(ctx, target.Tenant, data)
	if err != nil {
		s.Logger.Warn("dispatch: keeping a rider's photo", "err", err)
		return true, s.waSay(ctx, c.ch, c.addr, "", tr(l, "rider.photo_failed"))
	}
	if err := s.Dispatch.SetProof(ctx, target.Tenant, target.Delivery, path, ""); err != nil {
		return true, err
	}
	return true, s.waSay(ctx, c.ch, c.addr, "", tr(l, "rider.photo_kept"))
}

// storeProof keeps a proof photo in the business's own storage, cleaned
// (upright, no metadata) and private.
func (s *Server) storeProof(ctx context.Context, tenant uuid.UUID, data []byte) (string, error) {
	if s.ProofStore != nil {
		return s.ProofStore(ctx, tenant, data)
	}
	if s.Media == nil {
		return "", errors.New("storage for photos is not set up")
	}
	if s.Tables != nil {
		if _, err := s.Tables.EnsureProject(ctx, tenant); err != nil && !errors.Is(err, datatables.ErrUnavailable) {
			return "", err
		}
	}
	f, err := s.Media.PutFile(ctx, tenant, data)
	return f.Path, err
}

// --- the owner's view and hand ---

// deliveryView is a delivery with its photo's link, while it may be seen.
type deliveryView struct {
	dispatch.Delivery
	ProofURL string `json:"proof_url,omitempty"`
}

func (s *Server) dispatchReady(w http.ResponseWriter, r *http.Request) bool {
	if !s.Dispatch.Ready() {
		s.fail(w, r, fmt.Errorf("%w: deliveries with riders need messages set up on this Taskiem", errConflict))
		return false
	}
	return true
}

// dispatchFail maps dispatch's refusals to plain answers.
func (s *Server) dispatchFail(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()
	for _, p := range []string{"dispatch: the order is not ready to dispatch: ", "dispatch: that does not fit the delivery now: ", "dispatch: invalid: ",
		"riders: invalid: ", "storefront: "} {
		msg = strings.ReplaceAll(msg, p, "")
	}
	switch {
	case errors.Is(err, dispatch.ErrNoDelivery), errors.Is(err, storefront.ErrNoOrder), errors.Is(err, dispatch.ErrNoDelivery):
		err = pgx.ErrNoRows
	case errors.Is(err, dispatch.ErrInvalid):
		err = fmt.Errorf("%w: %s", errBadRequest, msg)
	case errors.Is(err, dispatch.ErrNotReady), errors.Is(err, dispatch.ErrMove), errors.Is(err, storefront.ErrDecision):
		err = fmt.Errorf("%w: %s", errConflict, msg)
	}
	s.riderFail(w, r, err)
}

func (s *Server) proofURLs(ctx context.Context, tenant uuid.UUID, list []dispatch.Delivery) []deliveryView {
	out := make([]deliveryView, len(list))
	for i, x := range list {
		out[i] = deliveryView{Delivery: x}
		if x.ProofPath() == "" || s.Media == nil {
			continue
		}
		if u, _, err := s.Media.SignURL(ctx, tenant, x.ProofPath(), 10*time.Minute); err == nil {
			out[i].ProofURL = u
		}
	}
	return out
}

// listDeliveries is GET /v1/deliveries: a day's (?day=2026-10-28, Lagos;
// default today) or the open ones (?open=true), newest first.
func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	f := dispatch.ListFilter{Open: r.URL.Query().Get("open") == "true", Day: r.URL.Query().Get("day")}
	if f.Day == "" && !f.Open {
		f.Day = time.Now().In(lagosTZ).Format("2006-01-02")
	}
	if f.Day != "" {
		if _, err := time.Parse("2006-01-02", f.Day); err != nil {
			s.fail(w, r, fmt.Errorf("%w: day is a date like 2026-10-28", errBadRequest))
			return
		}
	}
	var list []dispatch.Delivery
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		list, err = s.Dispatch.ListTx(r.Context(), tx, p.TenantID, f)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": s.proofURLs(r.Context(), p.TenantID, list)})
}

// orderDeliveries is GET /v1/storefront/orders/{reference}/deliveries: who
// was offered the order and who holds it, with why it cannot be offered
// now, if it cannot.
func (s *Server) orderDeliveries(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var list []dispatch.Delivery
	var why string
	err := s.tx(r, func(tx pgx.Tx) error {
		ord, err := s.Orders.GetOrder(r.Context(), tx, p.TenantID, chi.URLParam(r, "reference"))
		if err != nil {
			return err
		}
		why = dispatch.ReadyReason(ord)
		list, err = s.Dispatch.ListTx(r.Context(), tx, p.TenantID, dispatch.ListFilter{Order: &ord.ID})
		return err
	})
	if err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": s.proofURLs(r.Context(), p.TenantID, list), "not_ready": why})
}

// offerOrder is POST /v1/storefront/orders/{reference}/offer: to a named
// rider, or by rotation.
func (s *Server) offerOrder(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	var req struct {
		RiderID *uuid.UUID `json:"rider_id"`
		FeeKobo *int64     `json:"fee_kobo"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	by := p.Actor()
	if req.RiderID == nil {
		by = "rotation"
	}
	out, err := s.Dispatch.Offer(r.Context(), p.TenantID, chi.URLParam(r, "reference"), dispatch.OfferIn{Rider: req.RiderID, FeeKobo: req.FeeKobo, By: by})
	if err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// releaseOrder is POST /v1/storefront/orders/{reference}/release.
func (s *Server) releaseOrder(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	x, err := s.Dispatch.Release(r.Context(), p.TenantID, chi.URLParam(r, "reference"), p.Actor())
	if err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": x})
}

// getDispatchSettings is GET /v1/dispatch/settings.
func (s *Server) getDispatchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var out dispatch.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = dispatch.GetSettings(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putDispatchSettings is PUT /v1/dispatch/settings.
func (s *Server) putDispatchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	var in dispatch.Settings
	if err := decodeBody(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var out dispatch.Settings
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = dispatch.SaveSettings(r.Context(), tx, p.TenantID, in, p.Actor())
		return err
	}); err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ridersOwed is GET /v1/riders/owed: the cash each rider owes.
func (s *Server) ridersOwed(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	p := principalFrom(r.Context())
	var out []dispatch.Owed
	if err := s.tx(r, func(tx pgx.Tx) error {
		var err error
		out, err = s.Dispatch.OwedTx(r.Context(), tx, p.TenantID)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"owed": out})
}

// riderRemit is POST /v1/riders/{id}/remit: cash a rider handed in, or a
// transfer from them, against what they owe, oldest first.
func (s *Server) riderRemit(w http.ResponseWriter, r *http.Request) {
	if !s.dispatchReady(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		AmountKobo int64  `json:"amount_kobo"`
		How        string `json:"how"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	var left int64
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := s.Riders.GetTx(r.Context(), tx, p.TenantID, id); err != nil {
			return err
		}
		var err error
		left, err = s.Dispatch.RemitTx(r.Context(), tx, p.TenantID, id, req.AmountKobo, req.How, nil, p.Actor())
		return err
	}); err != nil {
		s.dispatchFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"owed_kobo": left})
}
