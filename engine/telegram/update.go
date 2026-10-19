package telegram

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
)

// SecretHeader carries the webhook's secret_token on every delivery.
const SecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // a header name, not a credential

// VerifySecret checks a delivery's SecretHeader against the secret given
// to setWebhook, in constant time. An empty secret accepts nothing.
func VerifySecret(header, secret string) bool {
	if secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(secret)) == 1
}

// Update is one webhook delivery (Bot API Update): a message, a tapped
// inline button, or something the channel ignores. Each delivery carries
// exactly one update.
type Update struct {
	ID        int64  // update_id: deduplication
	Kind      string // "message", "callback" or "other"
	FromID    int64  // the sender's user id
	Username  string // from.username, may be empty
	Language  string // from.language_code
	ChatID    int64
	ChatType  string // "private", "group", "supergroup", "channel"
	MessageID int64  // the message, or the one carrying the tapped button
	Text      string // message text (or caption)
	// CallbackID is the callback query to answer; CallbackData the
	// button's callback_data.
	CallbackID   string
	CallbackData string
	// Voice is a voice note or audio file (message.voice or message.audio):
	// only its file id arrives; the recording is fetched with getFile.
	Voice *Voice
}

// Voice is a recording's reference.
type Voice struct {
	FileID   string
	MimeType string
	Duration int   // seconds
	Size     int64 // 0 when not reported
	Voice    bool  // recorded in Telegram as a voice note (not an audio file)
}

// Private reports whether the update came from a one-to-one chat with the
// bot: the only place the channel answers.
func (u Update) Private() bool {
	return u.ChatType == "private" && u.ChatID == u.FromID && u.FromID > 0
}

type tgUser struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
	Language string `json:"language_code"`
}

type tgFile struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	Duration int    `json:"duration"`
	Size     int64  `json:"file_size"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text    string  `json:"text"`
	Caption string  `json:"caption"`
	Voice   *tgFile `json:"voice"`
	Audio   *tgFile `json:"audio"`
}

// Parse reads a webhook body. Updates other than a new message or a
// callback query (edits, channel posts, member changes) come back as
// Kind "other", as do messages from bots or without a sender.
func Parse(body []byte) (Update, error) {
	var d struct {
		UpdateID *int64     `json:"update_id"`
		Message  *tgMessage `json:"message"`
		Callback *struct {
			ID      string     `json:"id"`
			From    *tgUser    `json:"from"`
			Message *tgMessage `json:"message"`
			Data    string     `json:"data"`
		} `json:"callback_query"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return Update{}, fmt.Errorf("telegram: parsing an update: %w", err)
	}
	if d.UpdateID == nil {
		return Update{}, errors.New("telegram: parsing an update: no update_id")
	}
	u := Update{ID: *d.UpdateID, Kind: "other"}
	setFrom := func(f *tgUser) bool {
		if f == nil || f.ID <= 0 || f.IsBot {
			return false
		}
		u.FromID, u.Username, u.Language = f.ID, f.Username, f.Language
		return true
	}
	switch {
	case d.Message != nil:
		m := d.Message
		if !setFrom(m.From) {
			return u, nil
		}
		u.Kind, u.ChatID, u.ChatType, u.MessageID = "message", m.Chat.ID, m.Chat.Type, m.MessageID
		u.Text = m.Text
		if u.Text == "" {
			u.Text = m.Caption
		}
		switch {
		case m.Voice != nil && m.Voice.FileID != "":
			u.Voice = &Voice{FileID: m.Voice.FileID, MimeType: m.Voice.MimeType, Duration: m.Voice.Duration, Size: m.Voice.Size, Voice: true}
			if u.Voice.MimeType == "" {
				u.Voice.MimeType = "audio/ogg" // voice notes are Opus in Ogg
			}
		case m.Audio != nil && m.Audio.FileID != "":
			u.Voice = &Voice{FileID: m.Audio.FileID, MimeType: m.Audio.MimeType, Duration: m.Audio.Duration, Size: m.Audio.Size}
		}
	case d.Callback != nil:
		c := d.Callback
		if c.ID == "" || !setFrom(c.From) {
			return u, nil
		}
		u.Kind, u.CallbackID, u.CallbackData = "callback", c.ID, c.Data
		if c.Message != nil {
			u.ChatID, u.ChatType, u.MessageID = c.Message.Chat.ID, c.Message.Chat.Type, c.Message.MessageID
		}
	}
	return u, nil
}
