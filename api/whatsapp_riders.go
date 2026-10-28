package api

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/reach"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Riders in chat (docs/dispatch.md D1): the owner's riders and add rider;
// and a rider's own words to the platform number (off, on, busy till a
// time), which apply to every business they ride for.

// addRiderCmd is "add rider <name> <phone> [areas]".
var addRiderCmd = regexp.MustCompile(`(?i)^\s*add\s+rider\s+(.+?)\s+(\+?[0-9][0-9 ()-]{6,}[0-9])\s*(.*?)\s*$`)

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
