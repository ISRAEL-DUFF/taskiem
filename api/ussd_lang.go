package api

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/ussd"
)

// Choosing a language on USSD (docs/languages.md). When more than one
// language is on for the tenant, and the menu starts with a menu screen,
// the session's opening screen ends with "0. Language" (0 is never an
// option there). The caller's choice is recorded for the number, like
// "language yoruba" on WhatsApp, and Taskiem's own screens and SMS follow
// it; the tenant's menu is its own words and does not change. The walk
// stays a pure function of what was typed: "0*3*1" is the language list,
// the third language chosen, then option 1 of the menu.

// ussdLangKey opens the language list from the opening screen.
const ussdLangKey = "0"

// ussdLangsOn are the languages on for a tenant and its settings (English
// alone when they cannot be read).
func (s *Server) ussdLangsOn(ctx context.Context, tenant uuid.UUID) (tenantChannel, []lang.Tag) {
	tc, err := s.tenantChannel(ctx, tenant)
	if err != nil {
		return tenantChannel{Default: lang.EN}, []lang.Tag{lang.EN}
	}
	return tc, s.langsOn(tc)
}

// ussdCallerLang is the language for Taskiem's own texts to a caller: the
// one the number chose (on USSD or WhatsApp) or was detected in, when it
// is on, else the tenant's default when on, else English.
func (s *Server) ussdCallerLang(ctx context.Context, tenant uuid.UUID, phone string) lang.Tag {
	tc, on := s.ussdLangsOn(ctx, tenant)
	return s.callerLangIn(ctx, tc, on, phone)
}

func (s *Server) callerLangIn(ctx context.Context, tc tenantChannel, on []lang.Tag, phone string) lang.Tag {
	l := lang.EN
	if slices.Contains(on, tc.Default) {
		l = tc.Default
	}
	if len(on) > 1 && phone != "" && ctx.Err() == nil {
		pref, _, err := s.numberLang(ctx, ussd.NormalPhone(phone))
		if err == nil && pref != "" && slices.Contains(on, pref) {
			l = pref
		}
	}
	return l
}

// ussdLocalFor is ussdLocal in the caller's language.
func (s *Server) ussdLocalFor(ctx context.Context, tenant uuid.UUID, phone, text string) string {
	id, ok := strings.CutPrefix(text, ussdMsg)
	if !ok {
		return text
	}
	return lang.ASCII(tr(s.ussdCallerLang(ctx, tenant, phone), id))
}

// ussdLangMenu is the language list for one callback.
type ussdLangMenu struct {
	on  []lang.Tag
	cur lang.Tag // the caller's language, or the one just chosen
	// note says which language was just chosen, above the opening screen.
	note string
}

// ussdLangMenuFor is the language list a callback offers, or nil: one
// language on, or a menu that starts by asking for input.
func (s *Server) ussdLangMenuFor(ctx context.Context, tenant uuid.UUID, phone string, menu *ussd.Menu) *ussdLangMenu {
	if start := menu.StartScreen(); start == nil || start.Type != ussd.TypeMenu {
		return nil
	}
	tc, on := s.ussdLangsOn(ctx, tenant)
	if len(on) < 2 {
		return nil
	}
	return &ussdLangMenu{on: on, cur: s.callerLangIn(ctx, tc, on, phone)}
}

// walk handles the language list at the start of a path. It returns the
// list to show (done true), or what remains of the path for the menu.
func (s *Server) ussdLangWalk(tenant uuid.UUID, phone string, lm *ussdLangMenu, path []string) (done bool, text string, rest []string) {
	if len(path) == 0 || strings.TrimSpace(path[0]) != ussdLangKey {
		return false, "", path
	}
	invalid := false
	for i := 1; i < len(path); i++ {
		in := strings.TrimSpace(path[i])
		if in == ussd.Back {
			return false, "", path[i+1:]
		}
		n, err := strconv.Atoi(in)
		if err != nil || n < 1 || n > len(lm.on) || in != strconv.Itoa(n) {
			invalid = true
			continue
		}
		chosen := lm.on[n-1]
		if chosen != lm.cur {
			// Recorded after the answer, like "language <name>" on WhatsApp.
			number := ussd.NormalPhone(phone)
			s.ussdBackground(func(ctx context.Context) {
				if err := s.setNumberLang(ctx, number, chosen, "command"); err != nil {
					s.Logger.Warn("ussd: recording a caller's language", "tenant", tenant, "err", err)
				}
			})
		}
		lm.cur = chosen
		if len(path) == i+1 {
			id := "ussd.language.set"
			if !lang.Default().Reviewed(chosen, id) {
				id = "ussd.language.set_draft"
			}
			lm.note = lang.ASCII(tr(chosen, id, "language", langName(chosen)))
		}
		return false, "", path[i+1:]
	}
	var b strings.Builder
	if invalid {
		b.WriteString(lang.ASCII(tr(lm.cur, "ussd.language.invalid")) + "\n")
	}
	b.WriteString(lang.ASCII(tr(lm.cur, "ussd.language.choose")))
	for i, t := range lm.on {
		b.WriteString("\n" + strconv.Itoa(i+1) + ". " + lang.ASCII(langName(t)))
	}
	b.WriteString("\n" + ussd.Back + ". " + lang.ASCII(tr(lm.cur, "ussd.language.back")))
	return true, b.String(), nil
}

// opening adds the language entry, and the line saying which language was
// just chosen, to the opening screen, each only while the screen stays
// within the menu's size.
func (lm *ussdLangMenu) opening(menu *ussd.Menu, text string) string {
	fits := func(t string) bool { return utf8.RuneCountInString(t) <= menu.Limit() }
	if lm.note != "" && fits(lm.note+"\n"+text) {
		text = lm.note + "\n" + text
	}
	if entry := "\n" + ussdLangKey + ". " + lang.ASCII(tr(lm.cur, "ussd.language.entry")); fits(text + entry) {
		text += entry
	}
	return text
}
