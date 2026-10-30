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
	// Photo is a photo (message.photo, its largest size, or an image sent
	// as a file), its caption in Text: fetched with getFile too.
	Photo *Photo
	// Document is any other file sent (message.document): a list brought
	// into a table. Fetched with getFile.
	Document *Document
	// Contact is a shared contact (message.contact). Own reports whether it
	// is the sender's own, shared with Telegram's "share my number"
	// button: then Telegram vouches for the number.
	Contact *Contact
	// Location is a shared location (message.location) as a maps link.
	Location string
}

// Contact is a shared phone contact.
type Contact struct {
	Phone string
	Own   bool
}

// Photo is a photo's reference.
type Photo struct {
	FileID   string
	MimeType string
	Size     int64 // 0 when not reported
}

// Document is a file's reference.
type Document struct {
	FileID   string
	MimeType string
	Name     string
	Size     int64 // 0 when not reported
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
	Name     string `json:"file_name"`
}

type tgPhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int64  `json:"file_size"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text     string        `json:"text"`
	Caption  string        `json:"caption"`
	Voice    *tgFile       `json:"voice"`
	Audio    *tgFile       `json:"audio"`
	Photo    []tgPhotoSize `json:"photo"`
	Document *tgFile       `json:"document"`
	Contact  *struct {
		Phone  string `json:"phone_number"`
		UserID int64  `json:"user_id"`
	} `json:"contact"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
}

// imageTypes are the files read as photos when sent uncompressed.
var imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

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
		case len(m.Photo) > 0:
			// Telegram sends each size it made; the largest is kept.
			best := m.Photo[0]
			for _, p := range m.Photo[1:] {
				if p.Width*p.Height > best.Width*best.Height {
					best = p
				}
			}
			if best.FileID != "" {
				u.Photo = &Photo{FileID: best.FileID, MimeType: "image/jpeg", Size: best.Size}
			}
		case m.Document != nil && m.Document.FileID != "" && imageTypes[m.Document.MimeType]:
			u.Photo = &Photo{FileID: m.Document.FileID, MimeType: m.Document.MimeType, Size: m.Document.Size}
		case m.Document != nil && m.Document.FileID != "":
			u.Document = &Document{FileID: m.Document.FileID, MimeType: m.Document.MimeType, Name: m.Document.Name, Size: m.Document.Size}
		case m.Contact != nil && m.Contact.Phone != "":
			u.Contact = &Contact{Phone: m.Contact.Phone, Own: m.Contact.UserID == u.FromID}
		}
		if m.Location != nil {
			u.Location = fmt.Sprintf("https://www.google.com/maps?q=%.6f,%.6f", m.Location.Latitude, m.Location.Longitude)
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
