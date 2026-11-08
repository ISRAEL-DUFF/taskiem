package whatsapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles"
)

// Media in and out (decision 0062, docs/integrations/whatsapp.md), from
// Meta's public Cloud API documentation: "Retrieve media URL" (GET
// /{media-id} → {url, mime_type, sha256, file_size}), "Download media" (a
// GET of that url with the same token), "Upload media" (POST
// /{phone-number-id}/media as a form: messaging_product, type, file), and
// the typing indicator (a read receipt with typing_indicator). A file
// travels as a run file's reference, never its bytes (docs/files.md).

// mediaHost is where Meta serves media from (engine/whatsapp.MediaHosts).
const mediaHost = "lookaside.fbsbx.com"

// maxMedia is the largest file Meta accepts for any message type (100 MB
// for documents); the run file store refuses anything above its own caps.
const maxMedia = 100 << 20

func noFiles() error {
	return fmt.Errorf("whatsapp: this run cannot keep files (no file store on this deployment): %w", effects.ErrFatal)
}

func (c *client) getMedia(ctx context.Context, req connector.Request) (connector.Response, error) {
	if req.Files == nil {
		return connector.Response{}, noFiles()
	}
	id := strings.TrimSpace(str(req.Input, "media_id"))
	if id == "" || strings.ContainsAny(id, "/?#") {
		return connector.Response{}, fmt.Errorf("whatsapp: media_id (from the inbound message) is required: %w", effects.ErrFatal)
	}
	var info struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		SHA256   string `json:"sha256"`
		FileSize any    `json:"file_size"`
	}
	if err := c.do(ctx, req, http.MethodGet, "/"+url.PathEscape(id), nil, &info); err != nil {
		return connector.Response{}, err
	}
	var size int64
	switch v := info.FileSize.(type) {
	case float64:
		size = int64(v)
	case string:
		size, _ = strconv.ParseInt(v, 10, 64)
	}
	if size > maxMedia {
		return connector.Response{}, fmt.Errorf("whatsapp: the media file is %d bytes, too large to keep: %w", size, effects.ErrFatal)
	}
	u, err := url.Parse(info.URL)
	base, _ := url.Parse(c.base)
	fromMeta := err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), mediaHost)
	fromGraph := err == nil && base != nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) // test fakes
	if !fromMeta && !fromGraph {
		return connector.Response{}, fmt.Errorf("whatsapp: the media URL is not on Meta's media host: %w", effects.ErrFatal)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: %w: %w", err, effects.ErrFatal)
	}
	hreq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.Credentials["access_token"]))
	resp, err := req.HTTP.Do(hreq)
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: media download: %w: %w", err, effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		// The URL lasts minutes: an expired one is fetched again by a retry,
		// which looks the media id up afresh.
		return connector.Response{}, fmt.Errorf("whatsapp: media download answered %s: %w", resp.Status, effects.ErrRetryable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMedia+1))
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: media download: %w: %w", err, effects.ErrRetryable)
	}
	if len(data) > maxMedia {
		return connector.Response{}, fmt.Errorf("whatsapp: the media file is too large to keep: %w", effects.ErrFatal)
	}
	if want := strings.TrimSpace(info.SHA256); want != "" {
		sum := sha256.Sum256(data)
		if !strings.EqualFold(want, hex.EncodeToString(sum[:])) && want != base64.StdEncoding.EncodeToString(sum[:]) {
			return connector.Response{}, fmt.Errorf("whatsapp: the media file does not match its digest: %w", effects.ErrRetryable)
		}
	}
	name := strings.TrimSpace(str(req.Input, "name"))
	if name == "" {
		name = "whatsapp-" + id + extFor(info.MimeType)
	}
	// What a customer sent: scanned and kept as personal data.
	ref, err := req.Files.Put(ctx, name, data, true)
	if err != nil {
		return connector.Response{}, runfiles.ActionError(err)
	}
	return connector.Response{Output: map[string]any{"file": ref, "mime_type": info.MimeType}}, nil
}

// extFor is a file name's extension for a media type.
func extFor(mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0])) {
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	case "audio/amr":
		return ".amr"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "application/pdf":
		return ".pdf"
	}
	return ""
}

// upload sends a run file to Meta and answers its media id.
func (c *client) upload(ctx context.Context, req connector.Request, file any, voice bool) (string, error) {
	if req.Files == nil {
		return "", noFiles()
	}
	pid, err := phoneNumberID(req)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(req.Credentials["access_token"])
	if tok == "" {
		return "", fmt.Errorf("whatsapp: the connection has no access_token: %w", effects.ErrFatal)
	}
	data, ref, err := req.Files.Read(ctx, file)
	if err != nil {
		return "", runfiles.ActionError(err)
	}
	ctype, _ := ref["type"].(string)
	if voice {
		// A voice note is Ogg with the Opus codec (Meta's audio docs);
		// plain audio/ogg is not accepted for one.
		if !strings.HasPrefix(ctype, "audio/ogg") {
			return "", fmt.Errorf("whatsapp: a voice note must be Ogg/Opus audio, not %s: %w", ctype, effects.ErrFatal)
		}
		ctype = "audio/ogg; codecs=opus"
	}
	name, _ := ref["name"].(string)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("messaging_product", "whatsapp")
	_ = mw.WriteField("type", ctype)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", ctype)
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("whatsapp: %w: %w", err, effects.ErrFatal)
	}
	_, _ = part.Write(data)
	_ = mw.Close()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+pid+"/media", &body)
	if err != nil {
		return "", fmt.Errorf("whatsapp: %w: %w", err, effects.ErrFatal)
	}
	hreq.Header.Set("Authorization", "Bearer "+tok)
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := req.HTTP.Do(hreq)
	if err != nil {
		// An upload repeated makes a second media id and nothing else.
		return "", fmt.Errorf("whatsapp: media upload: %w: %w", err, effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return "", classify(&connector.HTTPError{Status: resp.StatusCode, Body: raw, Header: resp.Header})
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("whatsapp: media upload answered without an id: %w", effects.ErrRetryable)
	}
	return out.ID, nil
}

func (c *client) uploadMedia(ctx context.Context, req connector.Request) (connector.Response, error) {
	voice, _ := req.Input["voice"].(bool)
	id, err := c.upload(ctx, req, req.Input["file"], voice)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"media_id": id}}, nil
}

// sendTyping marks the customer's message read and shows "typing…" until
// the reply is sent, for at most 25 seconds.
func (c *client) sendTyping(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := phoneNumberID(req)
	if err != nil {
		return connector.Response{}, err
	}
	msg := strings.TrimSpace(str(req.Input, "message_id"))
	if msg == "" {
		return connector.Response{}, fmt.Errorf("whatsapp: message_id (the wamid of the message being answered) is required: %w", effects.ErrFatal)
	}
	var r struct {
		Success bool `json:"success"`
	}
	body := map[string]any{"messaging_product": "whatsapp", "status": "read", "message_id": msg, "typing_indicator": map[string]any{"type": "text"}}
	if err := c.do(ctx, req, http.MethodPost, "/"+id+"/messages", body, &r); err != nil {
		return connector.Response{}, err
	}
	if !r.Success {
		return connector.Response{}, fmt.Errorf("whatsapp: typing indicator returned success false: %w", effects.ErrRetryable)
	}
	return connector.Response{Output: map[string]any{"success": true}}, nil
}

// errNoMedia is a send_media without exactly one source.
var errNoMedia = errors.New("whatsapp: give exactly one of link, media_id and file")
