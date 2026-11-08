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
	"github.com/israel-duff/taskiem/engine/media"
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

// metaMediaHost reports whether a host is one Meta serves media from: its
// media host, or its CDN (*.fbcdn.net, *.whatsapp.net) should the media
// URL redirect there. Nothing else is followed.
func metaMediaHost(h string) bool {
	h = strings.ToLower(h)
	return h == mediaHost || strings.HasSuffix(h, ".fbcdn.net") || strings.HasSuffix(h, ".whatsapp.net")
}

// maxMedia is the largest file a run keeps (media.MaxFileBytes); a voice
// note may be at most media.MaxVoiceBytes. A larger download is refused
// before it is read into memory.
const maxMedia = media.MaxFileBytes

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
	// Asked on the connection's own number: Meta answers only for media
	// sent to that number, so another business's media id is not fetched.
	pid, err := phoneNumberID(req)
	if err != nil {
		return connector.Response{}, err
	}
	if err := c.do(ctx, req, http.MethodGet, "/"+url.PathEscape(id)+"?phone_number_id="+pid, nil, &info); err != nil {
		return connector.Response{}, err
	}
	var size int64
	switch v := info.FileSize.(type) {
	case float64:
		size = int64(v)
	case string:
		size, _ = strconv.ParseInt(v, 10, 64)
	}
	limit := int64(maxMedia)
	if strings.HasPrefix(strings.ToLower(info.MimeType), "audio/") {
		limit = media.MaxVoiceBytes
	}
	if size > limit {
		return connector.Response{}, fmt.Errorf("whatsapp: the media file is %d bytes, too large to keep: %w", size, effects.ErrFatal)
	}
	u, err := url.Parse(info.URL)
	base, _ := url.Parse(c.base)
	fromMeta := err == nil && u.Scheme == "https" && metaMediaHost(u.Hostname())
	fromGraph := err == nil && base != nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) // test fakes
	if !fromMeta && !fromGraph {
		return connector.Response{}, fmt.Errorf("whatsapp: the media URL is not on Meta's media host: %w", effects.ErrFatal)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: %w: %w", err, effects.ErrFatal)
	}
	hreq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.Credentials["access_token"]))
	// A redirect is followed only to Meta's own hosts (the egress guard
	// still checks each one against the manifest's hosts).
	hc := *req.HTTP
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || r.URL.Scheme != "https" || !metaMediaHost(r.URL.Hostname()) {
			return fmt.Errorf("whatsapp: media redirected to %s, not one of Meta's hosts", r.URL.Hostname())
		}
		r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.Credentials["access_token"]))
		return nil
	}
	if fromGraph {
		hc.CheckRedirect = nil
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: media download: %w: %w", err, effects.ErrRetryable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		// The URL lasts minutes: an expired one is fetched again by a retry,
		// which looks the media id up afresh.
		return connector.Response{}, fmt.Errorf("whatsapp: media download answered %s: %w", resp.Status, effects.ErrRetryable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return connector.Response{}, fmt.Errorf("whatsapp: media download: %w: %w", err, effects.ErrRetryable)
	}
	if int64(len(data)) > limit {
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
	// What a customer sent: scanned, kept as personal data, and tied to
	// the sender so erasing them deletes it.
	var about []connector.FileSubject
	if from := strings.TrimSpace(str(req.Input, "sender")); from != "" {
		about = append(about, connector.FileSubject{Category: "phone", Value: from})
	}
	ref, err := req.Files.Put(ctx, name, data, true, about...)
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
	if voice && (!strings.HasPrefix(ctype, "audio/ogg") || !media.IsOpus(data)) {
		// Meta takes Ogg with the Opus codec only for a voice note (and
		// lists audio/ogg as "OPUS codecs only"): Vorbis is refused here,
		// not by Meta after the upload.
		return "", fmt.Errorf("whatsapp: a voice note must be Ogg/Opus audio, not %s: %w", ctype, effects.ErrFatal)
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
	typing := true
	err = c.do(ctx, req, http.MethodPost, "/"+id+"/messages", body, &r)
	if err != nil && errors.Is(err, effects.ErrFatal) && strings.Contains(strings.ToLower(err.Error()), "typing") {
		// A Graph version or account without typing indicators refuses the
		// field: the read receipt alone is still worth sending.
		delete(body, "typing_indicator")
		typing = false
		err = c.do(ctx, req, http.MethodPost, "/"+id+"/messages", body, &r)
	}
	if err != nil {
		return connector.Response{}, err
	}
	if !r.Success {
		return connector.Response{}, fmt.Errorf("whatsapp: typing indicator returned success false: %w", effects.ErrRetryable)
	}
	return connector.Response{Output: map[string]any{"success": true, "typing": typing}}, nil
}

// errNoMedia is a send_media without exactly one source.
var errNoMedia = errors.New("whatsapp: give exactly one of link, media_id and file")
