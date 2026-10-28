package api

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Riders in chat (docs/dispatch.md D1): the owner's riders and add rider;
// and a rider's own words to the platform number (off, on, busy till a
// time), which apply to every business they ride for.

// addRiderCmd is "add rider <name> <phone> [areas]".
var addRiderCmd = regexp.MustCompile(`(?i)^\s*add\s+rider\s+(.+?)\s+(\+?[0-9][0-9 ()-]{6,}[0-9])\s*(.*?)\s*$`)

// The owner's dispatch commands: "order AK-7QX4KD to Musa" (or to any
// rider), "release AK-7QX4KD", "deliveries".
var (
	offerCmd   = regexp.MustCompile(`^order ` + bookingRef + ` to (.+)$`)
	releaseCmd = regexp.MustCompile(`^release ` + bookingRef + `$`)
)

// waRidersCommand answers the owner's riders and add rider; false for
// anything else.
func (s *Server) waRidersCommand(ctx context.Context, c *chat, text, cmd string) (bool, error) {
	if !s.Riders.Ready() {
		return false, nil
	}
	var do func() error
	switch {
	case cmd == "riders" || cmd == "my riders":
		do = func() error { return s.waRiders(ctx, c) }
	case cmd == "add rider" || strings.HasPrefix(cmd, "add rider "):
		do = func() error { return s.waAddRider(ctx, c, text) }
	case s.Dispatch.Ready() && offerCmd.MatchString(cmd):
		m := offerCmd.FindStringSubmatch(cmd)
		do = func() error { return s.waOfferOrder(ctx, c, strings.ToUpper(m[1]), m[2]) }
	case s.Dispatch.Ready() && releaseCmd.MatchString(cmd):
		m := releaseCmd.FindStringSubmatch(cmd)
		do = func() error { return s.waReleaseOrder(ctx, c, strings.ToUpper(m[1])) }
	case s.Dispatch.Ready() && (cmd == "deliveries" || cmd == "my deliveries" || cmd == "today's deliveries"):
		do = func() error { return s.waDeliveries(ctx, c) }
	default:
		return false, nil
	}
	if !c.p.Can(PermStorefrontManage) {
		return true, c.say(ctx, s, c.t("wa.shop.denied", "org", c.tenant.Name))
	}
	return true, do()
}

// riderLine is one rider in the owner's list: name, areas, state, and
// where they are reached.
func riderLine(c *chat, x riders.Rider) string {
	state := c.t("wa.riders." + x.Presence.State)
	if x.Presence.State == "busy" && x.Presence.Until != nil {
		state = c.t("wa.riders.busy", "time", riders.Clock(*x.Presence.Until))
	}
	if !x.Active {
		state = c.t("wa.riders.away")
	}
	where := c.t("wa.riders.by_sms")
	if x.Reached != "sms" {
		where = c.t("wa.riders.on_app", "app", appName(x.Reached))
	}
	line := "• " + x.Name
	if len(x.Zones) > 0 {
		line += " (" + strings.Join(x.Zones, ", ") + ")"
	}
	return line + ": " + state + ", " + where
}

func appName(channel string) string {
	if channel == reach.Telegram {
		return "Telegram"
	}
	return "WhatsApp"
}

func (s *Server) waRiders(ctx context.Context, c *chat) error {
	var list []riders.Rider
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = s.Riders.ListTx(ctx, tx, c.tenant.ID, riders.Filter{})
		return err
	}); err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.riders.none"))
	}
	lines := []string{c.t("wa.riders.head")}
	for _, x := range list {
		lines = append(lines, riderLine(c, x))
	}
	return c.say(ctx, s, strings.Join(lines, "\n")+"\n\n"+c.t("wa.riders.tail"))
}

func (s *Server) waAddRider(ctx context.Context, c *chat, text string) error {
	m := addRiderCmd.FindStringSubmatch(text)
	if m == nil {
		return c.say(ctx, s, c.t("wa.riders.add_how"))
	}
	name, phone := m[1], m[2]
	var x riders.Rider
	err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		known, err := storefront.Zones(ctx, tx, c.tenant.ID)
		if err != nil {
			return err
		}
		zones := riders.SplitZones(m[3], known)
		x, err = s.Riders.AddTx(ctx, tx, c.tenant.ID, riders.In{Name: &name, Phone: &phone, Zones: &zones}, c.p.Actor())
		return err
	})
	switch {
	case errors.Is(err, riders.ErrExists):
		return c.say(ctx, s, c.t("wa.riders.exists", "name", x.Name))
	case errors.Is(err, riders.ErrInvalid):
		return c.say(ctx, s, c.t("wa.riders.refused", "reason", strings.TrimPrefix(err.Error(), "riders: invalid: ")))
	case err != nil:
		return err
	}
	if x.Reached != "sms" {
		return c.say(ctx, s, c.t("wa.riders.added_reached", "name", x.Name, "app", appName(x.Reached)))
	}
	return c.say(ctx, s, c.t("wa.riders.added", "name", x.Name))
}

// riderWord takes a rider's off, on, free or busy (till a time) from an
// address linked to no member; false when the words are none of those or
// the sender rides for no one.
func (s *Server) riderWord(ctx context.Context, ch chatChannel, channel, addr, text string) (bool, error) {
	if !s.Riders.Ready() {
		return false, nil
	}
	said, ok := riders.ParseWord(text, time.Now())
	if !ok {
		return false, nil
	}
	phone := ""
	if channel == reach.WhatsApp {
		phone = addr // WhatsApp proves the number a message comes from
	}
	n, err := s.Riders.Say(ctx, phone, channel, addr, said)
	if err != nil || n == 0 {
		return false, err
	}
	l := s.chooseLang(ctx, ch.OwnTenant(), addr, "").Tag
	switch said.State {
	case "off":
		return true, s.waSay(ctx, ch, addr, "", tr(l, "rider.off"))
	case "busy":
		return true, s.waSay(ctx, ch, addr, "", tr(l, "rider.busy", "time", riders.Clock(said.Until)))
	}
	return true, s.waSay(ctx, ch, addr, "", tr(l, "rider.on"))
}

// waOfferOrder offers an order to the rider named (the start of a
// rider's name, whatever the case), or by rotation to any rider.
func (s *Server) waOfferOrder(ctx context.Context, c *chat, ref, who string) error {
	who = strings.TrimSpace(who)
	in := dispatch.OfferIn{By: c.p.Actor()}
	if who == "any rider" || who == "anyone" || who == "any" {
		in.By = "rotation"
	} else {
		var list []riders.Rider
		if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
			var err error
			list, err = s.Riders.ListTx(ctx, tx, c.tenant.ID, riders.Filter{})
			return err
		}); err != nil {
			return err
		}
		var found []riders.Rider
		for _, r := range list {
			if strings.HasPrefix(strings.ToLower(r.Name), who) {
				found = append(found, r)
			}
		}
		switch len(found) {
		case 0:
			return c.say(ctx, s, c.t("wa.dispatch.no_such_rider", "name", who))
		case 1:
			in.Rider = &found[0].ID
		default:
			names := make([]string, len(found))
			for i, r := range found {
				names[i] = r.Name
			}
			return c.say(ctx, s, c.t("wa.dispatch.which_rider", "names", strings.Join(names, ", ")))
		}
	}
	out, err := s.Dispatch.Offer(ctx, c.tenant.ID, ref, in)
	switch {
	case errors.Is(err, storefront.ErrNoOrder):
		return c.say(ctx, s, c.t("wa.shop.no_order", "ref", ref))
	case errors.Is(err, dispatch.ErrNotReady), errors.Is(err, dispatch.ErrInvalid):
		return c.say(ctx, s, c.t("wa.dispatch.refused", "reason", strings.TrimPrefix(strings.TrimPrefix(err.Error(), "dispatch: the order is not ready to dispatch: "), "dispatch: invalid: ")))
	case err != nil:
		return err
	}
	switch out.Status {
	case "held":
		return c.say(ctx, s, c.t("wa.dispatch.held", "ref", ref, "rider", out.Rider))
	case "no_rider":
		return c.say(ctx, s, c.t("wa.dispatch.no_rider", "ref", ref))
	}
	return c.say(ctx, s, c.t("wa.dispatch.offered", "ref", ref, "rider", out.Rider))
}

// waReleaseOrder takes an order back from its rider.
func (s *Server) waReleaseOrder(ctx context.Context, c *chat, ref string) error {
	x, err := s.Dispatch.Release(ctx, c.tenant.ID, ref, c.p.Actor())
	switch {
	case errors.Is(err, storefront.ErrNoOrder), errors.Is(err, dispatch.ErrNoDelivery):
		return c.say(ctx, s, c.t("wa.dispatch.nothing_to_release", "ref", ref))
	case errors.Is(err, dispatch.ErrMove):
		return c.say(ctx, s, c.t("wa.dispatch.refused", "reason", strings.TrimPrefix(err.Error(), "dispatch: that does not fit the delivery now: ")))
	case err != nil:
		return err
	}
	return c.say(ctx, s, c.t("wa.dispatch.released", "ref", ref, "rider", x.Rider))
}

// waDeliveries lists today's deliveries, the late ones first.
func (s *Server) waDeliveries(ctx context.Context, c *chat) error {
	var list []dispatch.Delivery
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		var err error
		list, err = s.Dispatch.ListTx(ctx, tx, c.tenant.ID, dispatch.ListFilter{Day: time.Now().In(lagosTZ).Format("2006-01-02")})
		return err
	}); err != nil {
		return err
	}
	if len(list) == 0 {
		return c.say(ctx, s, c.t("wa.dispatch.none_today"))
	}
	slices.SortStableFunc(list, func(a, b dispatch.Delivery) int {
		if (a.Late != "") != (b.Late != "") {
			if a.Late != "" {
				return -1
			}
			return 1
		}
		return 0
	})
	lines := []string{c.t("wa.dispatch.today")}
	for _, x := range list {
		state := c.t("wa.dispatch.state." + x.Status)
		if x.Late != "" {
			state = c.t("wa.dispatch.late_"+x.Late) + ", " + state
		}
		lines = append(lines, "• "+x.Reference+" · "+x.Rider+": "+state)
	}
	return c.say(ctx, s, strings.Join(lines, "\n"))
}
