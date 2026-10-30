// Package telegram is Taskiem's own Telegram channel beside WhatsApp
// (decision 0035, docs/telegram.md): the operator's bot, through which
// people who linked their Telegram account approve, trigger and check runs,
// and receive notifications. It is separate from the telegram@1 connector,
// which tenants use inside workflows with their own bots.
//
// This package holds the Bot API client (calls through the egress guard),
// webhook parsing and the secret check, tg: addresses, and the person-level
// records behind the channel, reached only through the taskiem_tg_*
// functions (00175). Built from Telegram's public Bot API reference.
//
// Every call is POST <base>/bot<token>/<method> with a JSON body. The
// token is part of the URL, so no error leaves this package with it.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/israel-duff/taskiem/engine/egress"
)

// DefaultAPIURL is the Bot API.
const DefaultAPIURL = "https://api.telegram.org"

// ErrBlocked: Telegram refused with 403, as when the person blocked the
// bot, deleted their account, or never started a chat with it.
var ErrBlocked = errors.New("telegram: the bot cannot write to this account (blocked or never started)")

// ErrFileTooLarge: the file is bigger than the caller takes.
var ErrFileTooLarge = errors.New("telegram: the file is too large")

// RetryAfterError is flood control (429): nothing was sent, and the call
// may go again after After (parameters.retry_after).
type RetryAfterError struct {
	After  time.Duration
	Method string
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("telegram: %s: flood control, retry after %s", e.Method, e.After)
}

// RetryAfterDelay is how long the engine waits before the next attempt.
func (e *RetryAfterError) RetryAfterDelay() time.Duration { return e.After }

// APIError is another refusal Telegram explained.
type APIError struct {
	Method      string
	Status      int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %s: %d %s", e.Method, e.Status, e.Description)
}

// InlineButton is an inline keyboard button: Data comes back as the
// callback query's data when tapped (1-64 bytes). A button with URL opens
// it instead: in the person's browser, or with WebApp as a Mini App inside
// Telegram, which hands the page Telegram's signed initData.
type InlineButton struct {
	Text, Data string
	URL        string
	WebApp     bool
}

// Client calls the Bot API as the platform bot.
type Client struct {
	// BaseURL is DefaultAPIURL unless overridden (tests).
	BaseURL string
	Token   string
	// Egress guards the calls; nil uses a default guard. HTTP, when set,
	// replaces the guarded client (tests).
	Egress *egress.Guard
	HTTP   *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultAPIURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) client(purpose string, timeout time.Duration) (*http.Client, error) {
	if c.HTTP != nil {
		return c.HTTP, nil
	}
	u, err := url.Parse(c.base())
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("telegram: bad Bot API URL")
	}
	g := c.Egress
	if g == nil {
		g = &egress.Guard{}
	}
	return g.Client(egress.Policy{Tenant: "platform", Hosts: []string{u.Hostname()}, Purpose: purpose}, timeout), nil
}

// redact hides the token in err's text, keeping what it wraps.
func (c *Client) redact(err error) error {
	if err == nil || c.Token == "" || !strings.Contains(err.Error(), c.Token) {
		return err
	}
	return &redacted{msg: strings.ReplaceAll(err.Error(), c.Token, "<bot_token>"), err: err}
}

type redacted struct {
	msg string
	err error
}

func (r *redacted) Error() string { return r.msg }
func (r *redacted) Unwrap() error { return r.err }

// transport strips the request URL (which holds the token) from a
// transport error.
func transport(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// call invokes one method and decodes "result" into out (nil: ignore it).
func (c *Client) call(ctx context.Context, method string, body map[string]any, out any) error {
	return c.redact(c.do(ctx, method, body, out))
}

func (c *Client) do(ctx context.Context, method string, body map[string]any, out any) error {
	if c.Token == "" {
		return errors.New("telegram: the platform bot is not configured")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, err)
	}
	hc, err := c.client("telegram_platform", 15*time.Second)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/bot"+c.Token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, transport(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, transport(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		Parameters  *struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	jsonErr := json.Unmarshal(data, &env)
	if resp.StatusCode/100 != 2 || !env.OK {
		desc := env.Description
		if jsonErr != nil || desc == "" {
			desc = http.StatusText(resp.StatusCode)
		}
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			after := time.Second
			if env.Parameters != nil && env.Parameters.RetryAfter > 0 {
				after = time.Duration(env.Parameters.RetryAfter) * time.Second
			}
			return &RetryAfterError{After: after, Method: method}
		case http.StatusForbidden:
			return fmt.Errorf("telegram: %s: %s: %w", method, desc, ErrBlocked)
		}
		return &APIError{Method: method, Status: resp.StatusCode, Description: desc}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram: %s: unreadable result: %w", method, err)
		}
	}
	return nil
}

// SendMessage sends text, its WhatsApp-style *bold* as bold entities and
// with no parse_mode, so nothing else in it is read as markup; without
// link previews, with an inline keyboard of rows when given. It returns the
// message id.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, rows [][]InlineButton) (int64, error) {
	if strings.TrimSpace(text) == "" {
		return 0, errors.New("telegram: sendMessage: empty text")
	}
	// WhatsApp-style *bold* becomes bold entities (format.go).
	plain, ents := Bold(text)
	plain = clip(plain, 4096)
	body := map[string]any{"chat_id": chatID, "text": plain, "link_preview_options": map[string]any{"is_disabled": true}}
	if ents = clipEntities(ents, 4095); len(ents) > 0 {
		body["entities"] = ents
	}
	if len(rows) > 0 {
		kb, err := keyboard(rows)
		if err != nil {
			return 0, fmt.Errorf("telegram: sendMessage: %w", err)
		}
		body["reply_markup"] = kb
	}
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	if err := c.call(ctx, "sendMessage", body, &m); err != nil {
		return 0, err
	}
	if m.MessageID == 0 {
		return 0, errors.New("telegram: sendMessage: accepted without a message id")
	}
	return m.MessageID, nil
}

// AskContact sends text with a one-time keyboard holding one button that
// shares the person's own phone number (request_contact): Telegram sends
// the number registered to the account, which a bot cannot see otherwise.
func (c *Client) AskContact(ctx context.Context, chatID int64, text, button string) error {
	plain, _ := Bold(text)
	body := map[string]any{"chat_id": chatID, "text": clip(plain, 4096), "reply_markup": map[string]any{
		"keyboard": [][]any{{map[string]any{"text": button, "request_contact": true}}}, "one_time_keyboard": true, "resize_keyboard": true}}
	return c.call(ctx, "sendMessage", body, nil)
}

// SendClearing sends text and takes away a keyboard sent with AskContact.
func (c *Client) SendClearing(ctx context.Context, chatID int64, text string) error {
	plain, _ := Bold(text)
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": clip(plain, 4096),
		"reply_markup": map[string]any{"remove_keyboard": true}}, nil)
}

// SendPhoto sends a photo by its public https URL (Telegram fetches it)
// with a caption, its *bold* as entities as in SendMessage, and an inline
// keyboard of rows when given. It returns the message id.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, photoURL, caption string, rows [][]InlineButton) (int64, error) {
	if !strings.HasPrefix(photoURL, "https://") {
		return 0, errors.New("telegram: sendPhoto: a photo is sent by its https URL")
	}
	body := map[string]any{"chat_id": chatID, "photo": photoURL}
	if strings.TrimSpace(caption) != "" {
		plain, ents := Bold(caption)
		body["caption"] = clip(plain, 1024)
		if ents = clipEntities(ents, 1023); len(ents) > 0 {
			body["caption_entities"] = ents
		}
	}
	if len(rows) > 0 {
		kb, err := keyboard(rows)
		if err != nil {
			return 0, fmt.Errorf("telegram: sendPhoto: %w", err)
		}
		body["reply_markup"] = kb
	}
	var m struct {
		MessageID int64 `json:"message_id"`
	}
	if err := c.call(ctx, "sendPhoto", body, &m); err != nil {
		return 0, err
	}
	if m.MessageID == 0 {
		return 0, errors.New("telegram: sendPhoto: accepted without a message id")
	}
	return m.MessageID, nil
}

// keyboard is an inline keyboard's reply_markup.
func keyboard(rows [][]InlineButton) (map[string]any, error) {
	kb := make([][]map[string]any, 0, len(rows))
	for _, r := range rows {
		row := make([]map[string]any, 0, len(r))
		for _, b := range r {
			switch {
			case b.URL != "" && b.Text != "" && strings.HasPrefix(b.URL, "https://"):
				if b.WebApp {
					row = append(row, map[string]any{"text": b.Text, "web_app": map[string]string{"url": b.URL}})
				} else {
					row = append(row, map[string]any{"text": b.Text, "url": b.URL})
				}
				continue
			case b.Data == "" || len(b.Data) > 64 || b.Text == "":
				return nil, errors.New("a button needs text and 1-64 bytes of data, or an https link")
			}
			row = append(row, map[string]any{"text": b.Text, "callback_data": b.Data})
		}
		kb = append(kb, row)
	}
	return map[string]any{"inline_keyboard": kb}, nil
}

// RemoveKeyboard takes the inline keyboard off a sent message, so a
// decision cannot be tapped twice. A message already without one is fine.
func (c *Client) RemoveKeyboard(ctx context.Context, chatID, messageID int64) error {
	err := c.call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chatID, "message_id": messageID,
		"reply_markup": map[string]any{"inline_keyboard": [][]any{}}}, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusBadRequest && strings.Contains(ae.Description, "message is not modified") {
		return nil
	}
	return err
}

// AnswerCallback stops the button's spinner, showing text (at most 200
// characters) when not empty.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	body := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		body["text"] = clip(text, 200)
	}
	return c.call(ctx, "answerCallbackQuery", body, nil)
}

// FileInfo looks a file id up (getFile): the path to download it from,
// valid for an hour, and its size (0 when not reported).
func (c *Client) FileInfo(ctx context.Context, fileID string) (string, int64, error) {
	if fileID == "" {
		return "", 0, errors.New("telegram: getFile: no file id")
	}
	var f struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return "", 0, err
	}
	if f.Path == "" {
		return "", 0, errors.New("telegram: getFile: no file path (the file may be over 20 MB)")
	}
	return f.Path, f.Size, nil
}

// Download fetches a file by its getFile path, refusing one larger than
// max bytes.
func (c *Client) Download(ctx context.Context, path string, max int64) ([]byte, error) {
	data, err := c.download(ctx, path, max)
	return data, c.redact(err)
}

func (c *Client) download(ctx context.Context, path string, max int64) ([]byte, error) {
	if c.Token == "" {
		return nil, errors.New("telegram: the platform bot is not configured")
	}
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\\") || strings.Contains(path, "..") {
		return nil, errors.New("telegram: bad file path")
	}
	hc, err := c.client("telegram_media", 30*time.Second)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/file/bot"+c.Token+"/"+path, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: file download: %w", transport(err))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: file download: %w", transport(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("telegram: file download answered %d", resp.StatusCode)
	}
	if resp.ContentLength > max {
		return nil, ErrFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("telegram: file download: %w", transport(err))
	}
	if int64(len(data)) > max {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

// SetWebhook points the bot's updates at webhookURL, sent with secret in
// SecretHeader: messages and callback queries only. Pending updates are
// kept.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	return c.call(ctx, "setWebhook", map[string]any{"url": webhookURL, "secret_token": secret,
		"allowed_updates": []string{"message", "callback_query"}, "drop_pending_updates": false}, nil)
}

// GetMe is the bot's username (checks the token).
func (c *Client) GetMe(ctx context.Context) (string, error) {
	var me struct {
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return "", err
	}
	return me.Username, nil
}

// clip shortens s to at most n UTF-16 code units (how Telegram counts).
func clip(s string, n int) string {
	units := 0
	for _, r := range s {
		units += utf16.RuneLen(r)
	}
	if units <= n {
		return s
	}
	var b strings.Builder
	units = 0
	for _, r := range s {
		l := utf16.RuneLen(r)
		if units+l > n-1 {
			break
		}
		units += l
		b.WriteRune(r)
	}
	return b.String() + "…"
}

// formatID renders a message id.
func formatID(id int64) string { return strconv.FormatInt(id, 10) }
