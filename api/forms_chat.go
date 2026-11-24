package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/notices"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// Form steps in WhatsApp and Telegram (docs/spec/device-fields.md §2, §3,
// D1). A form step to members, or to the person working a business link,
// is put to them in their chat app too, once it is under To fill in; they
// answer its location with the chat's own location message and its photo
// with a photo, each `sent`, and a signature is unavailable there. What
// they send is kept until the question is whole, then sent as any answer.

// formButtonPrefix starts a form step's chat buttons: fq:send:<request>
// sends an answer weaker than a field's require_source as it is.
const formButtonPrefix = "fq:"

// FormAsk puts a form step to people in their chat apps (forms.Service.Ask).
func (s *Server) FormAsk(ctx context.Context, a forms.ChatAsk) {
	// A run's starter: in the chat they started it from (forms_starter.go).
	if s.askStarter(ctx, a) {
		return
	}
	users := a.Users
	if a.Role != "" {
		users = s.roleUsers(ctx, a.Tenant, a.Role, uuid.Nil)
	}
	if len(users) == 0 {
		return
	}
	path := "/forms/requests/" + a.Request.String()
	app := strings.TrimRight(s.formsAppURL(), "/") + path
	title := "To fill in: " + a.Title
	if a.Through {
		title = a.From + " asks: " + a.Title
	}
	body := formAskWords(a.Schema, app)
	type told struct {
		channel, addr string
		user          uuid.UUID
	}
	var to []told
	err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{a.Tenant}, func(tx pgx.Tx) error {
		if !a.Through {
			// A link's person has the alert the door left; members by name
			// or role a notice of their own.
			if err := notices.AddTx(ctx, tx, a.Tenant, users, notices.Notice{Kind: notices.Setup, Title: title, Body: "Answer it in the app, on the web, or in WhatsApp or Telegram.",
				Link: path, Group: "form:" + a.Request.String()}); err != nil {
				return err
			}
		}
		for _, channel := range []string{"whatsapp", "telegram"} {
			addrs, err := chatAddrsTx(ctx, tx, channel, users)
			if err != nil {
				return err
			}
			for _, u := range users {
				if addr, ok := addrs[u]; ok {
					to = append(to, told{channel, addr, u})
				}
			}
		}
		return nil
	})
	if err != nil {
		s.Logger.Warn("forms: asking in chat", "tenant", a.Tenant, "err", err)
		return
	}
	for _, t := range to {
		ch, err := s.chatChannelFor(ctx, t.channel, a.Tenant)
		if err != nil {
			continue
		}
		if _, err := ch.Send(ctx, t.addr, linkPhoneMessage(a.Tenant, ch.Prefix(""), title, body, nil)); err != nil {
			s.Logger.Warn("forms: a chat message", "tenant", a.Tenant, "err", err)
			continue
		}
		s.armCode(ctx, a, t.user, t.channel)
	}
}

// armCode marks a question's code as asked of user in channel, when the
// message just sent asks for it (a question whose only chat answer left
// is a code): their next message there is taken as it (g-scan-wa).
func (s *Server) armCode(ctx context.Context, a forms.ChatAsk, user uuid.UUID, channel string) {
	if channel != forms.AppWhatsApp && channel != forms.AppTelegram || !forms.ChatAnswerable(a.Schema) {
		return
	}
	field := forms.CodeField(a.Schema, nil)
	if field == "" {
		return
	}
	tenant := a.Asking
	if tenant == uuid.Nil {
		tenant = a.Tenant
	}
	if err := s.Forms.ChatArm(ctx, tenant, a.Request, user, field, channel); err != nil {
		s.Logger.Warn("forms: asking for a code in chat", "tenant", tenant, "err", err)
	}
}

func (s *Server) formsAppURL() string {
	if s.Forms != nil && s.Forms.AppURL != "" {
		return s.Forms.AppURL
	}
	return s.PublicURL
}

// formAskWords says how to answer a question in a chat: each location and
// photo it asks, with why, and where else to answer it.
func formAskWords(schema map[string]any, app string) string {
	if !forms.ChatAnswerable(schema) {
		return "Answer it in the app or on the web: " + app
	}
	props, _ := schema["properties"].(map[string]any)
	var lines []string
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		why, _ := p["why"].(string)
		if why != "" {
			why = ", " + why
		}
		switch wd.DeviceKind(p) {
		case wd.DeviceLocation:
			lines = append(lines, "Send your location here (attach, then Location)"+why+".")
		case wd.DevicePhoto:
			lines = append(lines, "Send a photo here"+why+".")
		case wd.DeviceSignature:
			lines = append(lines, "A signature cannot be given in a chat: it is left out.")
		case wd.DeviceScan:
			switch {
			case p["allow_typed"] == false:
				lines = append(lines, tr(lang.EN, "wa.form.code_scan_only"))
			case forms.CodeField(schema, nil) != "":
				// Nothing else to send first: the code is the next message.
				lines = append(lines, tr(lang.EN, "wa.form.code_now", "why", strings.TrimPrefix(why, ", ")))
			default:
				lines = append(lines, tr(lang.EN, "wa.form.code_then", "why", strings.TrimPrefix(why, ", ")))
			}
		}
	}
	if len(forms.WeakerInChat(schema)) > 0 {
		lines = append(lines, "This question wants your phone's own reading: what you send here is marked unverified unless you answer in the app.")
	}
	lines = append(lines, "Or answer it in the app: "+app)
	return strings.Join(lines, "\n")
}

var mapsAtRe = regexp.MustCompile(`[?&]q=(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?)`)

// latLngOf reads a shared location's maps link.
func latLngOf(link string) (float64, float64, bool) {
	m := mapsAtRe.FindStringSubmatch(link)
	if m == nil {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(m[1], 64)
	lng, err2 := strconv.ParseFloat(m[2], 64)
	return lat, lng, err1 == nil && err2 == nil
}

// formChatAnswer takes a location or a photo sent in WhatsApp or Telegram
// as the answer to the form step open for the person, and "send" (or its
// button) for an answer waiting on require_source. It reports whether
// the message was for a form step.
func (s *Server) formChatAnswer(ctx context.Context, c *chat) (bool, error) {
	if s.Forms == nil || c.p == nil || c.p.UserID == uuid.Nil {
		return false, nil
	}
	channel := c.ch.Name()
	if channel != forms.AppWhatsApp && channel != forms.AppTelegram {
		return false, nil
	}
	// A code asked for here: only the person's next message is it
	// (g-scan-wa); whatever that message is, the asking ends with it.
	if q, field, err := s.Forms.ChatCodeWaiting(ctx, forms.ChatAsker{Tenant: c.tenant.ID, User: c.p.UserID, Roles: c.p.Roles}, channel); err != nil {
		return false, err
	} else if q != nil && codeWords(c) {
		added, err := s.Forms.ChatAdd(ctx, *q, c.p.UserID, field, map[string]any{"text": strings.TrimSpace(c.in.Text)})
		if errors.Is(err, forms.ErrNotWaiting) {
			return true, c.say(ctx, s, q.Title+" has been answered already.")
		}
		if err != nil {
			return true, err
		}
		return true, s.formChatAdded(ctx, c, *q, added)
	}
	pressed, isPress := strings.CutPrefix(c.in.Reply, formButtonPrefix+"send:")
	send := isPress || (c.in.Type == "text" && normalizeCommand(c.in.Text) == "send")
	kind := ""
	switch {
	case c.in.Location != "":
		kind = wd.DeviceLocation
	case c.in.Image != nil:
		kind = wd.DevicePhoto
	case !send:
		return false, nil
	}
	qs, err := s.Forms.ChatQuestions(ctx, forms.ChatAsker{Tenant: c.tenant.ID, User: c.p.UserID, Roles: c.p.Roles})
	if err != nil {
		return false, err
	}
	if send {
		for _, q := range qs {
			if q.Waiting && (!isPress || q.Request.String() == pressed) {
				return true, s.formChatSubmit(ctx, c, q)
			}
		}
		if isPress {
			return true, c.say(ctx, s, "That question is no longer waiting for an answer.")
		}
		return false, nil
	}
	var q *forms.ChatQuestion
	for i := range qs {
		if !forms.ChatAnswerable(qs[i].Schema) {
			continue
		}
		if f := forms.FieldOf(qs[i].Schema, kind); f != "" {
			if q == nil || (q.Held[forms.FieldOf(q.Schema, kind)] != nil && qs[i].Held[f] == nil) {
				q = &qs[i]
			}
		}
	}
	if q == nil {
		return false, nil
	}
	field := forms.FieldOf(q.Schema, kind)
	var value map[string]any
	if kind == wd.DeviceLocation {
		lat, lng, ok := latLngOf(c.in.Location)
		if !ok {
			return true, c.say(ctx, s, "That location did not come through. Send it again.")
		}
		value = map[string]any{"lat": lat, "lng": lng}
	} else {
		value = map[string]any{"media": map[string]any{"channel": channel, "id": c.in.Image.ID, "mime": c.in.Image.MimeType, "sha": c.in.Image.SHA256}}
	}
	// Kept under the request's lock, what the person already sent read
	// again under it: two messages arriving together are decided one after
	// the other, and the question is sent once, whole.
	added, err := s.Forms.ChatAdd(ctx, *q, c.p.UserID, field, value)
	if errors.Is(err, forms.ErrNotWaiting) {
		return true, c.say(ctx, s, q.Title+" has been answered already.")
	}
	if err != nil {
		return true, err
	}
	return true, s.formChatAdded(ctx, c, *q, added)
}

// formChatAdded says what a question still waits for once a chat added to
// it (asking for the code, when only the code is left, as the person's
// next message), or holds it for require_source, or sends it.
func (s *Server) formChatAdded(ctx context.Context, c *chat, q forms.ChatQuestion, added forms.ChatAdded) error {
	if !added.Whole {
		have := map[string]bool{}
		for k := range added.Question.Held {
			have[k] = true
		}
		if code := forms.CodeField(q.Schema, have); code != "" {
			if err := s.Forms.ChatArm(ctx, q.Tenant, q.Request, c.p.UserID, code, c.ch.Name()); err != nil {
				return err
			}
			props, _ := q.Schema["properties"].(map[string]any)
			p, _ := props[code].(map[string]any)
			why, _ := p["why"].(string)
			return c.say(ctx, s, c.t("wa.form.code_next", "title", whatsapp.SafeText(q.Title), "why", why))
		}
		return c.say(ctx, s, "Got it, for "+q.Title+". "+formStillWanted(q.Schema, have))
	}
	if added.Weak {
		app := strings.TrimRight(s.formsAppURL(), "/") + "/forms/requests/" + q.Request.String()
		return c.say(ctx, s, q.From+" wants your phone's own reading for "+q.Title+": what was sent in a chat may be from anywhere. "+
			"Answer it in the app instead: "+app+"\nOr send it as it is, marked unverified.",
			whatsapp.Button{ID: formButtonPrefix + "send:" + q.Request.String(), Title: "Send as it is"})
	}
	return s.formChatSubmit(ctx, c, added.Question)
}

// formStillWanted says which location or photo a question still waits for.
func formStillWanted(schema map[string]any, have map[string]bool) string {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		if have[name] {
			continue
		}
		switch wd.DeviceKind(p) {
		case wd.DeviceLocation:
			return "Now send your location."
		case wd.DevicePhoto:
			return "Now send the photo."
		}
	}
	return ""
}

// formChatSubmit sends a question a chat has answered whole: its photos
// fetched from the chat app, through the forms' file rules.
func (s *Server) formChatSubmit(ctx context.Context, c *chat, q forms.ChatQuestion) error {
	answers := map[string]any{}
	var uploads []forms.Upload
	for field, v := range q.Held {
		m, _ := v.(map[string]any)
		ref, ok := m["media"].(map[string]any)
		if !ok {
			answers[field] = m
			continue
		}
		id, _ := ref["id"].(string)
		mime, _ := ref["mime"].(string)
		sha, _ := ref["sha"].(string)
		channel, _ := ref["channel"].(string)
		data, err := s.fetchHeldMedia(ctx, c, channel, &whatsapp.Media{ID: id, MimeType: mime, SHA256: sha})
		if err != nil {
			s.Logger.Warn("forms: fetching a photo sent in chat", "err", err)
			if err := s.Forms.ChatClear(ctx, q, c.p.UserID); err != nil {
				return err
			}
			return c.say(ctx, s, "The photo did not come through. Send it again.")
		}
		uploads = append(uploads, forms.Upload{Meta: forms.FileMeta{Field: field, Name: "photo", Type: http.DetectContentType(data), Size: int64(len(data))}, Data: data})
	}
	a := forms.ChatAnswer{Question: q, Channel: c.ch.Name(), User: c.p.UserID, Answers: answers, Uploads: uploads}
	if q.Through {
		a.Business = c.tenant.ID
	}
	status, note, err := s.Forms.ChatSubmit(ctx, a)
	for _, u := range uploads {
		clear(u.Data)
	}
	if err != nil {
		return err
	}
	if status == "refused" {
		if err := s.Forms.ChatClear(ctx, q, c.p.UserID); err != nil {
			return err
		}
		if note == forms.NoLongerTaking {
			// Answered by another message, or in the app, meanwhile.
			return c.say(ctx, s, q.Title+" has been answered already.")
		}
		return c.say(ctx, s, "That could not be sent: "+note+". Send it again.")
	}
	return c.say(ctx, s, "Sent to "+q.From+": "+q.Title+". Thank you.")
}

// fetchHeldMedia fetches a photo held for a question from the chat app it
// was sent in, which may not be the one the message completing the
// question came in on (a photo on Telegram, the location on WhatsApp).
func (s *Server) fetchHeldMedia(ctx context.Context, c *chat, channel string, m *whatsapp.Media) ([]byte, error) {
	from := c
	if channel != "" && channel != c.ch.Name() {
		from = &chat{tenant: c.tenant}
		switch channel {
		case forms.AppWhatsApp:
			if s.WhatsApp == nil {
				return nil, errors.New("whatsapp is off")
			}
			wa, err := s.WhatsApp.ForTenant(ctx, c.tenant.ID)
			if err != nil {
				return nil, err
			}
			from.wa = wa
		case forms.AppTelegram:
			// fetchMedia fetches from Telegram when there is no WhatsApp.
		default:
			return nil, errors.New("a photo from an unknown chat")
		}
	}
	data, _, _, err := s.fetchMedia(ctx, from, m, media.MaxImageBytes)
	return data, err
}
