// Package telegramtest is a fake Telegram Bot API for tests: it accepts the
// platform bot's calls under its token, records them, serves one file
// through getFile and the file endpoint, can refuse the next send as
// Telegram would (403 blocked, 429 flood control), and builds webhook
// updates as Telegram would send them.
package telegramtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/telegram"
)

// Call is one call the fake accepted.
type Call struct {
	Method    string // sendMessage, sendPhoto, editMessageReplyMarkup, answerCallbackQuery, setWebhook
	ChatID    int64
	MessageID int64                     // the sent or edited message
	Text      string                    // a message's text, or a photo's caption
	Photo     string                    // sendPhoto: the photo's URL
	Entities  []telegram.Entity         // formatting (bold), as sent
	Keyboard  [][]telegram.InlineButton // the inline keyboard (empty when removed)
	// AskContact: a reply keyboard with a "share my number" button.
	AskContact bool
	// answerCallbackQuery
	CallbackID string
	// setWebhook
	URL            string
	Secret         string
	AllowedUpdates []string
}

type file struct {
	path string
	data []byte
	size int64 // reported size; 0 reports the real one
}

// Bot is the fake Bot API.
type Bot struct {
	*httptest.Server
	Token    string
	Username string // what getMe answers

	mu        sync.Mutex
	calls     []Call
	nextID    int64
	failNext  int // status for the next sendMessage
	retry     int // retry_after for a 429
	blocked   map[int64]bool
	files     map[string]file // by file id
	downloads int
}

// New starts a fake for a bot token.
func New(t testing.TB, token string) *Bot {
	b := &Bot{Token: token, Username: "taskiem_test_bot", blocked: map[int64]bool{}, files: map[string]file{}}
	b.Server = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.Close)
	return b
}

// FailNext makes the next sendMessage fail: 403 (the person blocked the
// bot) or 429 (flood control, with retryAfter seconds).
func (b *Bot) FailNext(status, retryAfter int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failNext, b.retry = status, retryAfter
}

// Block makes every send to a chat fail with 403 until unblocked.
func (b *Bot) Block(chatID int64, blocked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.blocked[chatID] = blocked
}

// SetFile makes a file id known, as if a person had sent that recording;
// reportSize, when not 0, is the size getFile claims.
func (b *Bot) SetFile(fileID, path string, data []byte, reportSize int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[fileID] = file{path: path, data: data, size: reportSize}
}

// Downloads counts file downloads.
func (b *Bot) Downloads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.downloads
}

// Calls lists the calls of one method (all with "").
func (b *Bot) Calls(method string) []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Call
	for _, c := range b.calls {
		if method == "" || c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Sent lists the messages sent to a chat (all with 0).
func (b *Bot) Sent(chatID int64) []Call {
	var out []Call
	for _, c := range b.Calls("sendMessage") {
		if chatID == 0 || c.ChatID == chatID {
			out = append(out, c)
		}
	}
	return out
}

// Last is the latest message sent to a chat; it fails the test if none.
func (b *Bot) Last(t testing.TB, chatID int64) Call {
	t.Helper()
	s := b.Sent(chatID)
	if len(s) == 0 {
		t.Fatalf("nothing sent to %d", chatID)
	}
	return s[len(s)-1]
}

// Reset forgets the calls.
func (b *Bot) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = nil
}

func answer(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func refuse(w http.ResponseWriter, status int, desc string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"ok": false, "error_code": status, "description": desc}
	if retryAfter > 0 {
		body["parameters"] = map[string]any{"retry_after": retryAfter}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (b *Bot) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if rest, ok := strings.CutPrefix(path, "/file/bot"+b.Token+"/"); ok && r.Method == http.MethodGet {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, f := range b.files {
			if f.path == rest {
				b.downloads++
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(f.data)
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	method, ok := strings.CutPrefix(path, "/bot"+b.Token+"/")
	if !ok || strings.Contains(method, "/") {
		refuse(w, http.StatusUnauthorized, "Unauthorized", 0) //nolint:misspell // Telegram's own wording
		return
	}
	if r.Method != http.MethodPost {
		refuse(w, http.StatusBadRequest, "Bad Request: the fake takes POST", 0)
		return
	}
	var in struct {
		ChatID      int64             `json:"chat_id"`
		MessageID   int64             `json:"message_id"`
		Text        string            `json:"text"`
		Entities    []telegram.Entity `json:"entities"`
		Photo       string            `json:"photo"`
		Caption     string            `json:"caption"`
		CaptionEnts []telegram.Entity `json:"caption_entities"`
		ParseMode   string            `json:"parse_mode"`
		ReplyMarkup *struct {
			InlineKeyboard [][]struct {
				Text         string `json:"text"`
				CallbackData string `json:"callback_data"`
				URL          string `json:"url"`
				WebApp       *struct {
					URL string `json:"url"`
				} `json:"web_app"`
			} `json:"inline_keyboard"`
			Keyboard [][]struct {
				Text           string `json:"text"`
				RequestContact bool   `json:"request_contact"`
			} `json:"keyboard"`
		} `json:"reply_markup"`
		CallbackQueryID string   `json:"callback_query_id"`
		FileID          string   `json:"file_id"`
		URL             string   `json:"url"`
		SecretToken     string   `json:"secret_token"`
		AllowedUpdates  []string `json:"allowed_updates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		refuse(w, http.StatusBadRequest, "Bad Request: "+err.Error(), 0)
		return
	}
	c := Call{Method: method, ChatID: in.ChatID, MessageID: in.MessageID, Text: in.Text, Entities: in.Entities, CallbackID: in.CallbackQueryID,
		URL: in.URL, Secret: in.SecretToken, AllowedUpdates: in.AllowedUpdates}
	if method == "sendPhoto" {
		c.Text, c.Entities, c.Photo = in.Caption, in.CaptionEnts, in.Photo
	}
	if in.ReplyMarkup != nil && len(in.ReplyMarkup.Keyboard) == 1 && len(in.ReplyMarkup.Keyboard[0]) == 1 && in.ReplyMarkup.Keyboard[0][0].RequestContact {
		c.AskContact = true
	} else if in.ReplyMarkup != nil {
		c.Keyboard = [][]telegram.InlineButton{}
		for _, row := range in.ReplyMarkup.InlineKeyboard {
			var bs []telegram.InlineButton
			for _, k := range row {
				if k.WebApp != nil && k.Text != "" && strings.HasPrefix(k.WebApp.URL, "https://") {
					bs = append(bs, telegram.InlineButton{Text: k.Text, URL: k.WebApp.URL, WebApp: true})
					continue
				}
				if k.URL != "" && k.Text != "" && strings.HasPrefix(k.URL, "https://") {
					bs = append(bs, telegram.InlineButton{Text: k.Text, URL: k.URL})
					continue
				}
				if k.Text == "" || len(k.CallbackData) == 0 || len(k.CallbackData) > 64 {
					refuse(w, http.StatusBadRequest, "Bad Request: BUTTON_DATA_INVALID", 0)
					return
				}
				bs = append(bs, telegram.InlineButton{Text: k.Text, Data: k.CallbackData})
			}
			c.Keyboard = append(c.Keyboard, bs)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch method {
	case "getMe":
		answer(w, map[string]any{"id": 1, "is_bot": true, "first_name": "Taskiem", "username": b.Username})
		return
	case "getFile":
		f, ok := b.files[in.FileID]
		if !ok {
			refuse(w, http.StatusBadRequest, "Bad Request: invalid file_id", 0)
			return
		}
		size := f.size
		if size == 0 {
			size = int64(len(f.data))
		}
		answer(w, map[string]any{"file_id": in.FileID, "file_unique_id": "u" + in.FileID, "file_size": size, "file_path": f.path})
		return
	case "sendMessage":
		if in.ChatID == 0 || in.Text == "" || len([]rune(in.Text)) > 4096 {
			refuse(w, http.StatusBadRequest, "Bad Request: message text is empty or too long", 0)
			return
		}
		if in.ParseMode != "" {
			refuse(w, http.StatusBadRequest, "Bad Request: the platform bot sends plain text", 0)
			return
		}
		fail, retry := b.failNext, b.retry
		b.failNext, b.retry = 0, 0
		switch {
		case fail == http.StatusForbidden || b.blocked[in.ChatID]:
			refuse(w, http.StatusForbidden, "Forbidden: bot was blocked by the user", 0)
			return
		case fail == http.StatusTooManyRequests:
			refuse(w, http.StatusTooManyRequests, fmt.Sprintf("Too Many Requests: retry after %d", retry), retry)
			return
		case fail != 0:
			refuse(w, fail, "refused by the fake", 0)
			return
		}
		b.nextID++
		c.MessageID = b.nextID
		b.calls = append(b.calls, c)
		answer(w, map[string]any{"message_id": c.MessageID, "chat": map[string]any{"id": in.ChatID, "type": "private"}, "date": 1759800000, "text": in.Text})
		return
	case "sendPhoto":
		if in.ChatID == 0 || !strings.HasPrefix(in.Photo, "https://") || len([]rune(in.Caption)) > 1024 {
			refuse(w, http.StatusBadRequest, "Bad Request: wrong photo or caption", 0)
			return
		}
		if b.blocked[in.ChatID] {
			refuse(w, http.StatusForbidden, "Forbidden: bot was blocked by the user", 0)
			return
		}
		b.nextID++
		c.MessageID = b.nextID
		b.calls = append(b.calls, c)
		answer(w, map[string]any{"message_id": c.MessageID, "chat": map[string]any{"id": in.ChatID, "type": "private"}, "date": 1759800000,
			"photo": []any{map[string]any{"file_id": "sent", "width": 320, "height": 320}}})
		return
	case "editMessageReplyMarkup":
		if in.ChatID == 0 || in.MessageID == 0 || in.MessageID > b.nextID {
			refuse(w, http.StatusBadRequest, "Bad Request: message to edit not found", 0)
			return
		}
		b.calls = append(b.calls, c)
		answer(w, map[string]any{"message_id": in.MessageID, "chat": map[string]any{"id": in.ChatID, "type": "private"}, "date": 1759800000})
		return
	case "answerCallbackQuery":
		if in.CallbackQueryID == "" {
			refuse(w, http.StatusBadRequest, "Bad Request: query is too old and response timeout expired or query ID is invalid", 0)
			return
		}
		b.calls = append(b.calls, c)
		answer(w, true)
		return
	case "setWebhook":
		if !strings.HasPrefix(in.URL, "https://") && !strings.HasPrefix(in.URL, "http://") {
			refuse(w, http.StatusBadRequest, "Bad Request: bad webhook: An HTTPS URL must be provided for webhook", 0)
			return
		}
		b.calls = append(b.calls, c)
		answer(w, true)
		return
	}
	refuse(w, http.StatusNotFound, "Not Found", 0)
}

// Webhook is the latest setWebhook call (zero if none).
func (b *Bot) Webhook() Call {
	c := b.Calls("setWebhook")
	if len(c) == 0 {
		return Call{}
	}
	return c[len(c)-1]
}

func from(id int64, username string) map[string]any {
	f := map[string]any{"id": id, "is_bot": false, "first_name": "Someone", "language_code": "en"}
	if username != "" {
		f["username"] = username
	}
	return f
}

func private(id int64) map[string]any {
	return map[string]any{"id": id, "type": "private", "first_name": "Someone"}
}

// Message is an update with a text message in the private chat of user id.
func Message(updateID, userID int64, username, text string) []byte {
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "message": map[string]any{
		"message_id": updateID, "from": from(userID, username), "chat": private(userID), "date": 1759800000, "text": text,
	}})
	return body
}

// ContactMessage is an update sharing a contact in the private chat of
// user id: its own (as the "share my number" button sends) when owner is
// userID.
func ContactMessage(updateID, userID int64, phone string, owner int64) []byte {
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "message": map[string]any{
		"message_id": updateID, "from": from(userID, ""), "chat": private(userID), "date": 1759800000,
		"contact": map[string]any{"phone_number": phone, "first_name": "Someone", "user_id": owner},
	}})
	return body
}

// Start is the /start <code> message a t.me link sends.
func Start(updateID, userID int64, username, code string) []byte {
	return Message(updateID, userID, username, "/start "+code)
}

// VoiceMessage is an update with a voice note (fileID, as SetFile made
// it known) in the private chat of user id.
func VoiceMessage(updateID, userID int64, fileID string, duration int) []byte {
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "message": map[string]any{
		"message_id": updateID, "from": from(userID, ""), "chat": private(userID), "date": 1759800000,
		"voice": map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "duration": duration, "mime_type": "audio/ogg"},
	}})
	return body
}

// PhotoMessage is an update with a photo (fileID, as SetFile made it
// known, in two sizes as Telegram sends them) and a caption in the
// private chat of user id.
func PhotoMessage(updateID, userID int64, fileID, caption string) []byte {
	msg := map[string]any{
		"message_id": updateID, "from": from(userID, ""), "chat": private(userID), "date": 1759800000,
		"photo": []any{
			map[string]any{"file_id": fileID + "-small", "file_unique_id": "s" + fileID, "width": 90, "height": 60, "file_size": 900},
			map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "width": 900, "height": 600},
		},
	}
	if caption != "" {
		msg["caption"] = caption
	}
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "message": msg})
	return body
}

// Photos lists the photos sent to a chat (all with 0).
func (b *Bot) Photos(chatID int64) []Call {
	var out []Call
	for _, c := range b.Calls("sendPhoto") {
		if chatID == 0 || c.ChatID == chatID {
			out = append(out, c)
		}
	}
	return out
}

// Callback is an update with a tapped inline button on messageID.
func Callback(updateID, userID, messageID int64, queryID, data string) []byte {
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "callback_query": map[string]any{
		"id": queryID, "from": from(userID, ""), "chat_instance": "1",
		"message": map[string]any{"message_id": messageID, "chat": private(userID), "date": 1759800000, "text": "…"},
		"data":    data,
	}})
	return body
}

// DocumentMessage is an update with a file (fileID, as SetFile made it
// known) of a name and type in the private chat of user id.
func DocumentMessage(updateID, userID int64, fileID, name, mime string) []byte {
	msg := map[string]any{
		"message_id": updateID, "from": from(userID, ""), "chat": private(userID), "date": 1759800000,
		"document": map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "file_name": name, "mime_type": mime},
	}
	body, _ := json.Marshal(map[string]any{"update_id": updateID, "message": msg})
	return body
}
