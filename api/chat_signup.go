package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai/builder"
	"github.com/israel-duff/taskiem/engine/ai/intent"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/telegram"
	"github.com/israel-duff/taskiem/engine/telemetry"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/templates"
)

// Signing up from chat (docs/spec/chat-way-in.md 6, decision 0058). A
// stranger on Taskiem's shared WhatsApp number or Telegram bot makes a
// business from the chat, beside the web form and field agents: the
// terms first (nothing is made before Yes), then the business's name,
// theirs, what they sell and how customers pay; on Telegram their own
// contact too, so every chat-born owner has a proven number. The business
// is made as a field agent's is (makeBusiness), the chat bound to its
// owner, and three templates offered that start without the AI. A person
// already linked makes another business with "new business" (or, with
// none left, is offered one), without the account steps. Until the
// business exists the answers wait in chat_signups, a person's record
// reached through narrow functions, for 30 minutes.

// ChatSignupConfig is who may sign up from chat.
type ChatSignupConfig struct {
	// Channels offer sign-up to strangers: whatsapp, telegram
	// (TASKIEM_CHAT_SIGNUP). Empty: off, the unbound message as before.
	Channels []string
	// PerHour is how many businesses chat may make an hour, every channel
	// and replica together (TASKIEM_CHAT_SIGNUP_PER_HOUR); 0 is 60.
	PerHour int
	// Countries are the calling codes sign-up is open to
	// (TASKIEM_CHAT_SIGNUP_COUNTRIES); empty is 234.
	Countries []string
}

// TermsConfig is the terms and privacy notice every way in asks people to
// agree to (TASKIEM_TERMS_URL, TASKIEM_TERMS_VERSION,
// TASKIEM_PRIVACY_VERSION). Without versions nothing is asked.
type TermsConfig struct {
	URL            string
	Version        string
	PrivacyVersion string
}

// On reports whether there are terms to agree to.
func (t TermsConfig) On() bool { return t.Version != "" && t.PrivacyVersion != "" }

const (
	chatSignupTTL = 30 * time.Minute
	// A number (or Telegram account) makes at most this many businesses a day.
	chatSignupPerIdentityDay = 3
	defaultChatSignupPerHour = 60

	suTerms    = "terms"
	suBusiness = "business"
	suName     = "name"
	suSells    = "sells"
	suPays     = "pays"
	suContact  = "contact" // Telegram: their own number
)

// What a business sells and how its customers pay, in the order they are
// listed (story 2).
var (
	signupSells = []string{"food", "provisions", "fashion", "phones", "school", "salon", "services", "other"}
	signupPays  = []string{"cash", "transfer", "pos", "paystack", "moniepoint", "opay"}
)

// signupState is a sign-up's answers so far.
type signupState struct {
	Step     string   `json:"-"`
	Business string   `json:"business,omitempty"`
	Name     string   `json:"name,omitempty"`
	Sells    string   `json:"sells,omitempty"`
	Pays     []string `json:"pays,omitempty"`
	// Suggest is a name offered for the business (the Slack workspace's).
	Suggest string `json:"suggest,omitempty"`
	// Agreed is when the person replied Yes to the terms.
	Agreed *time.Time `json:"agreed,omitempty"`
}

// signupChat is a message in a sign-up.
type signupChat struct {
	ch   chatChannel
	addr string
	// user is the person when the address is linked (no business left, or
	// another business): no terms, no name, no binding.
	user uuid.UUID
	lang lang.Tag
	// tg is the Telegram account writing, on Telegram.
	tg *telegram.Update
	// slack is the Slack user writing and the token their business's
	// Slack connection takes, on Slack (slack_install.go).
	slack *slackSignup
}

func (sc *signupChat) t(id string, kv ...string) string { return tr(sc.lang, id, kv...) }

func (s *Server) suSay(ctx context.Context, sc *signupChat, text string, buttons ...whatsapp.Button) error {
	return s.waSay(ctx, sc.ch, sc.addr, "", text, buttons...)
}

// signupOn reports whether a channel offers sign-up: the operator turned
// it on, the terms are set, and it is a shared channel (a stranger on a
// business's own number is its customer).
func (s *Server) signupOn(ch chatChannel) bool {
	return ch != nil && ch.OwnTenant() == uuid.Nil && s.Terms.On() && slices.Contains(s.ChatSignup.Channels, ch.Name())
}

func signupKey(addr string) []byte {
	h := sha256.Sum256([]byte("chat-signup:" + addr))
	return h[:]
}

// identityHash is how a sign-up's identity (its proven number, or the
// address) is counted toward the daily limit.
func identityHash(id string) []byte {
	h := sha256.Sum256([]byte("chat-identity:" + id))
	return h[:]
}

func (s *Server) signupLoad(ctx context.Context, addr string) (*signupState, error) {
	var st signupState
	var raw []byte
	err := s.Store.Pool.QueryRow(ctx, `SELECT step, data FROM taskiem_chat_signup_get($1)`, signupKey(addr)).Scan(&st.Step, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, json.Unmarshal(raw, &st)
}

func (s *Server) signupSave(ctx context.Context, sc *signupChat, st *signupState) error {
	raw, _ := json.Marshal(st)
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_signup_put($1, $2, $3, $4, $5::interval)`, signupKey(sc.addr), sc.ch.Name(), st.Step, raw, chatSignupTTL.String())
	return err
}

func (s *Server) signupDrop(ctx context.Context, addr string) error {
	_, err := s.Store.Pool.Exec(ctx, `SELECT taskiem_chat_signup_drop($1)`, signupKey(addr))
	return err
}

func signupCount(channel, outcome string) {
	telemetry.Signups.WithLabelValues(channel, outcome).Inc()
}

// signupWord reports whether a message asks to start a business. A
// linked person's "start" may mean something else: only the longer words.
func signupWord(text string, linked bool) bool {
	// Not "/start": opening the Telegram bot gets the offer, which also
	// has "I already use Taskiem".
	w := strings.Join(strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!"))), " ")
	switch w {
	case "new business", "start my business", "start a business":
		return true
	case "start", "sign up", "signup", "register", "start signup":
		return !linked
	}
	return false
}

// signupPhrase reports whether a message asks to start a business in the
// person's language ("I wan open account"): the word lists' signup
// intent. A linked person's English is signupWord's alone.
func signupPhrase(text string, l lang.Tag, linked bool) bool {
	m, ok := lang.MatchIntent(text, []lang.Tag{l, lang.EN})
	return ok && m.Intent == lang.IntentSignup && (!linked || m.Language != lang.EN)
}

// signupModelRoute asks the model whether a stranger's free text means
// "sign me up" (docs/spec/chat-way-in.md 11): that intent only, a few
// times an hour per chat, on the platform's account (there is no
// business yet to charge).
func (s *Server) signupModelRoute(ctx context.Context, sc *signupChat, text string) bool {
	text = strings.TrimSpace(text)
	if s.Languages == nil || s.Languages.Model == nil || sc.user != uuid.Nil || len(strings.Fields(text)) < 2 {
		return false
	}
	if !s.limiter("su-model:"+sc.addr, time.Hour, 5).Allow() {
		return false
	}
	info, _ := lang.Lookup(sc.lang)
	cl := &intent.Classifier{Provider: s.Languages.Model, Intents: intent.Stranger}
	res, _, err := cl.Classify(ctx, text, info)
	if err != nil {
		if !errors.Is(err, intent.ErrUnusable) {
			s.Logger.Warn("chat sign-up: routing a message through the model", "err", err)
		}
		return false
	}
	return res.Intent == lang.IntentSignup
}

// chatSignupMessage handles a message from an address in a sign-up, a word
// or button that starts one, or the offer's other buttons; false leaves
// the message to what comes next.
func (s *Server) chatSignupMessage(ctx context.Context, sc *signupChat, text, reply string) (bool, error) {
	if !s.signupOn(sc.ch) {
		return false, nil
	}
	st, err := s.signupLoad(ctx, sc.addr)
	if err != nil {
		return true, err
	}
	if st != nil {
		return true, s.signupStep(ctx, sc, st, text, reply)
	}
	switch {
	case reply == "su:start" || signupWord(text, sc.user != uuid.Nil) || signupPhrase(text, sc.lang, sc.user != uuid.Nil):
		return true, s.signupBegin(ctx, sc)
	case reply == "su:have" && sc.user == uuid.Nil:
		// Linking an account made on the web, by a link to its email
		// (chat_doors.go).
		return true, s.chatLinkAsk(ctx, sc)
	case reply == "su:no":
		return true, s.suSay(ctx, sc, sc.t("signup.later"))
	}
	return false, nil
}

// signupOffer answers a stranger nothing else claimed: what Taskiem is,
// and whether they want to start. A language's name typed chooses it;
// free text that means "sign me up" starts at once.
func (s *Server) signupOffer(ctx context.Context, sc *signupChat, text string) error {
	if info, ok := lang.Find(strings.TrimSpace(text)); ok && slices.Contains(s.operatorLangs(), info.Tag) {
		if err := s.setNumberLang(ctx, sc.addr, info.Tag, "command"); err != nil {
			return err
		}
		sc.lang = info.Tag
	} else if s.signupModelRoute(ctx, sc, text) {
		return s.signupBegin(ctx, sc)
	}
	return s.suSay(ctx, sc, sc.t("signup.offer", "languages", langNames(s.operatorLangs())),
		whatsapp.Button{ID: "su:start", Title: sc.t("signup.button_start")},
		whatsapp.Button{ID: "su:have", Title: sc.t("signup.button_have")},
		whatsapp.Button{ID: "su:no", Title: sc.t("signup.button_no")})
}

// signupOpen checks the country and the limits before a business is
// begun or made: "" when it may go on, else what to say.
func (s *Server) signupOpen(ctx context.Context, sc *signupChat, number string) (string, error) {
	// The countries are where sign-up is open: a person with an account
	// makes another business wherever they are.
	if number != "" && sc.user == uuid.Nil {
		codes := s.ChatSignup.Countries
		if len(codes) == 0 {
			codes = []string{"234"}
		}
		if !slices.ContainsFunc(codes, func(c string) bool { return strings.HasPrefix(number, "+"+c) }) {
			signupCount(sc.ch.Name(), "country")
			return sc.t("signup.country"), nil
		}
	}
	id := number
	if id == "" {
		id = sc.addr
	}
	var mine, all int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT identity_day, all_hour FROM taskiem_chat_signup_counts($1)`, identityHash(id)).Scan(&mine, &all); err != nil {
		return "", err
	}
	perHour := s.ChatSignup.PerHour
	if perHour <= 0 {
		perHour = defaultChatSignupPerHour
	}
	switch {
	case mine >= chatSignupPerIdentityDay:
		signupCount(sc.ch.Name(), "rate_limited")
		return sc.t("signup.too_many", "count", strconv.Itoa(chatSignupPerIdentityDay)), nil
	case all >= perHour:
		signupCount(sc.ch.Name(), "rate_limited")
		s.Logger.Warn("chat sign-up: the hourly limit is reached (TASKIEM_CHAT_SIGNUP_PER_HOUR)", "limit", perHour)
		return sc.t("signup.busy"), nil
	}
	return "", nil
}

// signupNumber is the number a sign-up is proven by, when the channel
// gives one before the end: the WhatsApp number writing.
func signupNumber(sc *signupChat) string {
	if sc.ch.Name() == "whatsapp" {
		return sc.addr
	}
	return ""
}

func (s *Server) signupBegin(ctx context.Context, sc *signupChat) error {
	if msg, err := s.signupOpen(ctx, sc, signupNumber(sc)); err != nil || msg != "" {
		if err != nil {
			return err
		}
		return s.suSay(ctx, sc, msg)
	}
	st := &signupState{Step: suTerms}
	if sc.user != uuid.Nil {
		st.Step = suBusiness
	}
	if ss := sc.slack; ss != nil {
		// Slack names the workspace and, for a new person, who they are.
		st.Suggest = ss.teamName
		if sc.user == uuid.Nil {
			st.Name = ss.person
		}
	}
	if err := s.signupSave(ctx, sc, st); err != nil {
		return err
	}
	return s.signupAsk(ctx, sc, st)
}

// signupAsk asks the step's question.
func (s *Server) signupAsk(ctx context.Context, sc *signupChat, st *signupState) error {
	switch st.Step {
	case suTerms:
		return s.suSay(ctx, sc, sc.t("signup.terms", "link", s.termsLink(), "languages", langNames(s.operatorLangs())),
			whatsapp.Button{ID: "su:yes", Title: sc.t("button.yes")}, whatsapp.Button{ID: "su:no", Title: sc.t("button.no")})
	case suBusiness:
		if n, ok := plainName(st.Suggest, 2, 80); ok {
			return s.suSay(ctx, sc, sc.t("signup.ask_business"), whatsapp.Button{ID: "su:suggest", Title: n})
		}
		return s.suSay(ctx, sc, sc.t("signup.ask_business"))
	case suName:
		return s.suSay(ctx, sc, sc.t("signup.ask_name"))
	case suSells:
		return s.suSay(ctx, sc, sc.t("signup.ask_sells"))
	case suPays:
		return s.suSay(ctx, sc, sc.t("signup.ask_pays"))
	case suContact:
		if sc.tg != nil && s.Telegram != nil {
			return s.Telegram.Client.AskContact(ctx, sc.tg.FromID, sc.t("signup.ask_contact"), sc.t("agent.share_button"))
		}
	case suEmail:
		return s.suSay(ctx, sc, sc.t("door.ask_email"))
	}
	return nil
}

func yesWord(w string) bool {
	switch w {
	case "yes", "y", "ok", "okay", "i agree", "agree", "yes i agree":
		return true
	}
	return false
}

// signupStep takes the answer to the step a sign-up is at.
func (s *Server) signupStep(ctx context.Context, sc *signupChat, st *signupState, text, reply string) error {
	word := strings.Join(strings.Fields(strings.ToLower(strings.Trim(strings.TrimSpace(text), "/.!"))), " ")
	stop := func(outcome, msg string) error {
		if err := s.signupDrop(ctx, sc.addr); err != nil {
			return err
		}
		signupCount(sc.ch.Name(), outcome)
		return s.suSay(ctx, sc, sc.t(msg))
	}
	if reply == "su:no" && st.Step == suTerms || st.Step == suTerms && (word == "no" || word == "n") {
		return stop("declined", "signup.declined")
	}
	if reply == "su:no" || word == "cancel" || word == "stop" {
		return stop("cancelled", "signup.cancelled")
	}
	// A language's name chooses it, except where a name is the answer.
	if st.Step != suBusiness && st.Step != suName {
		if info, ok := lang.Find(word); ok && slices.Contains(s.operatorLangs(), info.Tag) {
			if err := s.setNumberLang(ctx, sc.addr, info.Tag, "command"); err != nil {
				return err
			}
			sc.lang = info.Tag
			return s.signupAsk(ctx, sc, st)
		}
	}
	next := func(step string) error {
		st.Step = step
		if err := s.signupSave(ctx, sc, st); err != nil {
			return err
		}
		return s.signupAsk(ctx, sc, st)
	}
	switch st.Step {
	case suTerms:
		if reply != "su:yes" && !yesWord(word) {
			return s.signupAsk(ctx, sc, st)
		}
		now := time.Now().UTC()
		st.Agreed = &now
		return next(suBusiness)
	case suBusiness:
		if reply == "su:suggest" && st.Suggest != "" {
			text = st.Suggest
		}
		name, ok := plainName(text, 2, 80)
		if !ok {
			return s.suSay(ctx, sc, sc.t("signup.business_bad"))
		}
		st.Business = name
		if sc.user != uuid.Nil || st.Name != "" {
			// Their name is known: the account's, or Slack's.
			return next(suSells)
		}
		return next(suName)
	case suName:
		name, ok := plainName(text, 1, 80)
		if !ok {
			return s.suSay(ctx, sc, sc.t("signup.name_bad"))
		}
		st.Name = name
		return next(suSells)
	case suSells:
		picked := signupPicks(word, signupSells)
		if len(picked) != 1 {
			return s.signupAsk(ctx, sc, st)
		}
		st.Sells = picked[0]
		return next(suPays)
	case suPays:
		picked := signupPicks(word, signupPays)
		if len(picked) == 0 {
			return s.signupAsk(ctx, sc, st)
		}
		st.Pays = picked
		if sc.ch.Name() == "telegram" && sc.user == uuid.Nil {
			return next(suContact)
		}
		return s.signupMake(ctx, sc, st, signupNumber(sc))
	case suContact:
		return s.signupAsk(ctx, sc, st)
	case suEmail:
		return s.chatLinkEmail(ctx, sc, text)
	}
	return stop("cancelled", "signup.cancelled")
}

// signupPicks reads choices from a list by number or by word: "2", "pos
// and transfer", "1, 3".
func signupPicks(answer string, list []string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' || r == '/' || r == '&' || r == '+' }) {
		pick := ""
		if n, err := strconv.Atoi(f); err == nil && n >= 1 && n <= len(list) {
			pick = list[n-1]
		} else if slices.Contains(list, f) {
			pick = f
		}
		if pick != "" && !slices.Contains(out, pick) {
			out = append(out, pick)
		}
	}
	return out
}

// chatSignupContact takes the contact a Telegram sign-up asked for: their
// own, in a country sign-up is open to, and a number no WhatsApp-linked
// person has (one person, many doors: linked, not a second account).
// false when no sign-up waits for it.
func (s *Server) chatSignupContact(ctx context.Context, sc *signupChat, c *telegram.Contact) (bool, error) {
	if !s.signupOn(sc.ch) || sc.tg == nil {
		return false, nil
	}
	st, err := s.signupLoad(ctx, sc.addr)
	if err != nil || st == nil || st.Step != suContact {
		return false, err
	}
	say := func(text string) error { return s.Telegram.Client.SendClearing(ctx, sc.tg.FromID, text) }
	if !c.Own {
		return true, say(sc.t("signup.contact_own"))
	}
	number, err := whatsapp.Normalise(c.Phone, s.defaultCountry())
	if err != nil {
		return true, say(sc.t("signup.contact_own"))
	}
	var taken bool
	if err := s.Store.Pool.QueryRow(ctx, `SELECT taskiem_chat_number_taken($1)`, number).Scan(&taken); err != nil {
		return true, err
	}
	if taken {
		// One person, many doors (chat_doors.go).
		if done, err := s.chatNumberDoor(ctx, sc, st, number); done || err != nil {
			return true, err
		}
		if err := s.signupDrop(ctx, sc.addr); err != nil {
			return true, err
		}
		signupCount(sc.ch.Name(), "exists")
		return true, say(sc.t("signup.number_taken", "where", s.accountWhere()))
	}
	return true, s.signupMake(ctx, sc, st, number)
}

// errSignupBind is a binding the sign-up's transaction could not make.
type errSignupBind struct{ outcome string }

func (e errSignupBind) Error() string { return "chat sign-up: binding: " + e.outcome }

// signupMake makes the business (and the person, and their binding) once
// every answer is in.
func (s *Server) signupMake(ctx context.Context, sc *signupChat, st *signupState, number string) error {
	if msg, err := s.signupOpen(ctx, sc, number); err != nil || msg != "" {
		if err != nil {
			return err
		}
		if err := s.signupDrop(ctx, sc.addr); err != nil {
			return err
		}
		return s.suSay(ctx, sc, msg)
	}
	channel := sc.ch.Name()
	id := number
	if id == "" {
		id = sc.addr
	}
	nb := newBusiness{Name: st.Business, OwnerName: st.Name, Owner: sc.user, Source: channel, ChannelHash: identityHash(id), ActorType: "user",
		Detail: map[string]any{"channel": channel, "sells": st.Sells, "pays": st.Pays}}
	if number != "" {
		nb.Detail["number"] = whatsapp.MaskNumber(number)
	}
	// On Slack the business gets the workspace's Slack, and its owner is
	// linked there, whoever they are (slack_install.go).
	var join func(tx pgx.Tx, tenant, owner uuid.UUID) error
	if ss := sc.slack; ss != nil {
		if err := s.slackSignupToken(ctx, ss); err != nil {
			return err
		}
		nb.Detail["workspace"] = ss.team
		join = func(tx pgx.Tx, tenant, owner uuid.UUID) error { return s.slackJoinTx(ctx, tx, tenant, owner, ss) }
		nb.Then = join
	}
	if sc.user == uuid.Nil {
		// A person the address is the only proof of. On WhatsApp the sign-in
		// name is the field agent's for the number, so an owner an agent
		// signed up who never agreed is found and is this one.
		nb.OwnerEmail = ownerPlaceholderEmail(number)
		switch channel {
		case "telegram":
			nb.OwnerEmail = telegramPlaceholderEmail(sc.tg.FromID)
		case "slack":
			// Slack's email, unconfirmed, unless it is someone's account.
			email, err := s.slackOwnerEmail(ctx, sc.slack)
			if err != nil {
				return err
			}
			nb.OwnerEmail = email
		}
		var found uuid.UUID
		err := s.Store.Pool.QueryRow(ctx, `SELECT user_id FROM taskiem_auth_find_user($1)`, nb.OwnerEmail).Scan(&found)
		switch {
		case err == nil:
			nb.Owner = found
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		nb.Detail["new_owner"] = nb.Owner == uuid.Nil
		nb.Then = func(tx pgx.Tx, tenant, owner uuid.UUID) error {
			outcome := "ok"
			var err error
			switch channel {
			case "telegram":
				err = tx.QueryRow(ctx, `SELECT taskiem_chat_bind_telegram($1, $2, $3)`, owner, sc.tg.FromID, sc.tg.Username).Scan(&outcome)
			case "slack":
				err = join(tx, tenant, owner)
			default:
				err = tx.QueryRow(ctx, `SELECT taskiem_chat_bind_whatsapp($1, $2)`, owner, number).Scan(&outcome)
			}
			if err != nil {
				return err
			}
			if outcome != "ok" {
				return errSignupBind{outcome}
			}
			return s.termsAcceptTx(ctx, tx, tenant, owner, channel, st.Agreed)
		}
	}
	tenant, _, err := s.makeBusiness(ctx, nb)
	if be := (errSignupBind{}); errors.As(err, &be) {
		if err := s.signupDrop(ctx, sc.addr); err != nil {
			return err
		}
		signupCount(channel, "exists")
		return s.suSay(ctx, sc, sc.t("signup.number_taken", "where", s.accountWhere()))
	}
	if err != nil {
		return err
	}
	if err := s.signupDrop(ctx, sc.addr); err != nil {
		return err
	}
	if err := sc.ch.Touch(ctx, sc.addr, false, true, &tenant); err != nil {
		return err
	}
	signupCount(channel, "created")
	return s.signupDone(ctx, sc, st)
}

// telegramPlaceholderEmail is the sign-in name of a person who signed up
// on Telegram: it receives no mail, and is the same for the account.
func telegramPlaceholderEmail(id int64) string {
	h := sha256.Sum256([]byte("taskiem-owner:tg:" + strconv.FormatInt(id, 10)))
	return fmt.Sprintf("owner+%x@telegram.taskiem.invalid", h[:8])
}

// termsAcceptTx records a person's Yes to the current terms and privacy
// notice, in a tenant's audit log too.
func (s *Server) termsAcceptTx(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, channel string, at *time.Time) error {
	if !s.Terms.On() {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT taskiem_terms_accept($1, $2, $3, $4)`, user, s.Terms.Version, s.Terms.PrivacyVersion, channel); err != nil {
		return err
	}
	if tenant == uuid.Nil {
		return nil // someone in no business yet, answering invitations
	}
	detail := map[string]any{"channel": channel, "terms": s.Terms.Version, "privacy": s.Terms.PrivacyVersion}
	if at != nil {
		detail["agreed_at"] = at.Format(time.RFC3339)
	}
	raw, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, 'user', $2, 'terms.accept', $2, $3)`, tenant, user.String(), raw)
	return err
}

// signupDone says the business is made, and offers three templates that
// fit it.
func (s *Server) signupDone(ctx context.Context, sc *signupChat, st *signupState) error {
	var list strings.Builder
	var buttons []whatsapp.Button
	for i, id := range signupTemplates(st.Sells, st.Pays) {
		tpl, ok := templates.Default().Get(id)
		if !ok {
			continue
		}
		fmt.Fprintf(&list, "%d. *%s*: %s\n", i+1, sc.t("signup.tpl."+id), tpl.Summary)
		buttons = append(buttons, whatsapp.Button{ID: signupTplPrefix + id, Title: sc.t("signup.tpl." + id)})
	}
	head := sc.t("signup.done", "business", whatsapp.SafeText(st.Business))
	if s.Billing.On() && s.Billing.Config.TrialDays > 0 {
		head = sc.t("signup.done_trial", "business", whatsapp.SafeText(st.Business), "days", strconv.Itoa(s.Billing.Config.TrialDays))
	}
	text := head + "\n\n" + sc.t("signup.starters") + "\n\n" + strings.TrimSpace(list.String()) + "\n\n" + sc.t("signup.own_words")
	if sc.ch.Name() == "whatsapp" && sc.user == uuid.Nil {
		text += "\n\n" + sc.t("signup.pin_hint")
	}
	return s.suSay(ctx, sc, text, buttons...)
}

const signupTplPrefix = "su:tpl:"

// signupTemplates are the three templates offered to a new business, by
// what it sells and how it is paid: the first needs nothing connected, so
// a run can come at once.
func signupTemplates(sells string, pays []string) []string {
	shop := slices.Contains([]string{"food", "provisions", "fashion", "phones", "other"}, sells)
	out := []string{"daily-reminder"}
	add := func(id string) {
		if len(out) < 3 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	switch {
	case sells == "school":
		add("school-fee-reminders")
	case shop:
		add("sales-day-total")
	default:
		add("send-invoice")
	}
	switch {
	case slices.Contains(pays, "pos"):
		add("pos-daily-summary")
	case slices.Contains(pays, "paystack"):
		add("payment-receipt-whatsapp")
	case shop:
		add("low-stock-alert")
	}
	add("send-invoice")
	add("stock-count-reminder")
	return out
}

// chatTemplateBuild starts a build from a template the person picked, with
// no AI: the template's defaults, then the build conversation asks what
// it still needs, shows the steps and saves on Yes (whatsapp_build.go).
func (s *Server) chatTemplateBuild(ctx context.Context, c *chat, id string) error {
	if !c.p.Can(PermWorkflowEdit) {
		return c.say(ctx, s, c.t("wa.build.denied", "org", c.tenant.Name))
	}
	tpl, ok := templates.Default().Get(id)
	if !ok || tpl.SupersededBy != "" {
		return c.say(ctx, s, c.t("wa.build.gone"))
	}
	params, missing := map[string]any{}, []string{}
	values := tpl.Examples()
	for _, p := range tpl.Params {
		switch {
		case p.Default != nil:
			params[p.Name], values[p.Name] = p.Default, p.Default
		case p.Required:
			missing = append(missing, p.Name)
		}
	}
	doc, err := tpl.Instantiate(values, "")
	if err != nil {
		return err
	}
	use := &builder.TemplateUse{ID: tpl.ID, Title: tpl.Title, Instantiated: true, Params: params, Missing: missing}
	prop := &builder.Proposal{Definition: doc, Summary: tpl.Summary, Template: use, Assumptions: []string{}, Problems: []builder.Problem{},
		Warnings: []builder.Warning{}, Results: []builder.TestResult{}, Connectors: tpl.Connectors, Tables: []builder.TableSpec{}, Needs: []builder.Need{}}
	raw, err := json.Marshal(prop)
	if err != nil {
		return err
	}
	build := uuid.Must(uuid.NewV7())
	if err := chatTx(ctx, s, c, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_builds (id, tenant_id, actor, goal, environment, status, stage, provider, model, proposal, rounds, valid,
			channel, template_id, finished_at) VALUES ($1, $2, $3, $4, '', 'proposed', 'done', 'template', 'template', $5, 0, true, $6, $7, now())`,
			build, c.tenant.ID, c.p.Actor(), "template "+tpl.ID, raw, c.ch.Name(), tpl.ID)
		return err
	}); err != nil {
		return err
	}
	c.data.Build = &waBuild{ID: build, Phase: buildBuilding}
	return s.waBuildAnswer(ctx, c, prop, nil)
}

// --- the business, for every way in but the web form ---

// newBusiness is a business made for its owner where a channel proves who
// they are: by a field agent, or from chat (docs/spec/chat-way-in.md 6.3).
type newBusiness struct {
	Name      string
	OwnerName string
	// Owner is the person when they have an account; else one is made with
	// OwnerEmail as its sign-in name and no password.
	Owner      uuid.UUID
	OwnerEmail string
	// Source is tenant_onboarding's; ChannelHash the identity's digest
	// for chat's daily limit.
	Source      string
	ChannelHash []byte
	// ActorType and Actor record tenant.create; a "user" with no Actor is
	// the owner.
	ActorType, Actor string
	Detail           map[string]any
	// Then runs last in the same transaction (the agent's session, the
	// chat's binding and terms).
	Then func(tx pgx.Tx, tenant, owner uuid.UUID) error
}

// makeBusiness makes the business, its default workspace and
// environments, its owner (and their account, when they have none) and
// its onboarding record in one transaction, then starts its trial.
func (s *Server) makeBusiness(ctx context.Context, nb newBusiness) (tenant, owner uuid.UUID, err error) {
	owner, tenant = nb.Owner, uuid.Must(uuid.NewV7())
	newOwner := owner == uuid.Nil
	if newOwner {
		owner = uuid.Must(uuid.NewV7())
	}
	actor := nb.Actor
	if actor == "" {
		actor = owner.String()
	}
	err = db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if newOwner {
			// No password: the owner works through the channel that proved them.
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, owner, nb.OwnerEmail, nb.OwnerName); err != nil {
				return err
			}
		}
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO tenants (id, name, plan_id) VALUES ($1, $2, $3)`, []any{tenant, nb.Name, uuid.Nil}},
			{`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{tenant, owner}},
			{`INSERT INTO workspaces (id, tenant_id, name) VALUES ($1, $2, 'Default')`, []any{uuid.Must(uuid.NewV7()), tenant}},
			{`INSERT INTO environments (tenant_id, name) VALUES ($1, 'dev'), ($1, 'prod')`, []any{tenant}},
			{`INSERT INTO tenant_onboarding (tenant_id, source, signed_up_by, channel_hash) VALUES ($1, $2, $3, $4)`, []any{tenant, nb.Source, owner, nb.ChannelHash}},
		}
		for _, st := range stmts {
			if _, err := tx.Exec(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		detail := map[string]any{}
		for k, v := range nb.Detail {
			detail[k] = v
		}
		raw, _ := json.Marshal(detail)
		if _, err := tx.Exec(ctx, `SELECT taskiem_audit_append($1, $2, $3, 'tenant.create', $4, $5)`, tenant, nb.ActorType, actor, tenant.String(), raw); err != nil {
			return err
		}
		if nb.Then != nil {
			return nb.Then(tx, tenant, owner)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	billingActor := actor
	if nb.ActorType == "user" {
		billingActor = "user:" + actor
	}
	return tenant, owner, s.Billing.Ensure(ctx, tenant, billingActor)
}
