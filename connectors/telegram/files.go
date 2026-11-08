package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles"
)

// Files in and out (decision 0062, docs/integrations/telegram.md), from the
// Bot API documentation: getFile answers a file_path, downloaded from
// https://api.telegram.org/file/bot<token>/<file_path> within an hour; a
// bot can download files up to 20 MB. sendVoice takes OGG/Opus, MP3 or
// M4A shown as a voice message; sendAudio an MP3 or M4A shown in the music
// player; sendChatAction shows "typing" or "recording a voice message" for
// up to 5 seconds. A file travels as a run file's reference, never its
// bytes (docs/files.md).

// maxDownload is the largest file a bot may download.
const maxDownload = 20 << 20

func noFiles() error {
	return fmt.Errorf("telegram: this run cannot keep files (no file store on this deployment): %w", effects.ErrFatal)
}

func (c *client) getFile(ctx context.Context, req connector.Request) (connector.Response, error) {
	if req.Files == nil {
		return connector.Response{}, noFiles()
	}
	fid := strings.TrimSpace(str(req.Input, "file_id"))
	if fid == "" {
		return connector.Response{}, fmt.Errorf("telegram: file_id (from the update's voice, audio, photo or document) is required: %w", effects.ErrFatal)
	}
	var r any
	if err := c.call(ctx, req, "getFile", map[string]any{"file_id": fid}, &r); err != nil {
		return connector.Response{}, err
	}
	m, _ := r.(map[string]any)
	path := str(m, "file_path")
	if path == "" {
		// Telegram gives no path for a file over 20 MB.
		return connector.Response{}, fmt.Errorf("telegram: the file cannot be downloaded by a bot (over 20 MB): %w", effects.ErrFatal)
	}
	if size, ok := intOf(m["file_size"]); ok && size > maxDownload {
		return connector.Response{}, fmt.Errorf("telegram: the file is %d bytes, over a bot's 20 MB: %w", size, effects.ErrFatal)
	}
	tok := strings.TrimSpace(req.Credentials["bot_token"])
	u := c.base + "/file/bot" + tok + "/" + (&url.URL{Path: path}).EscapedPath()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return connector.Response{}, scrub(fmt.Errorf("telegram: %w: %w", err, effects.ErrFatal), tok)
	}
	resp, err := req.HTTP.Do(hreq)
	if err != nil {
		return connector.Response{}, scrub(fmt.Errorf("telegram: file download: %w: %w", connector.RedactURLError(err), effects.ErrRetryable), tok)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		// The link lasts an hour: a retry asks getFile again.
		return connector.Response{}, fmt.Errorf("telegram: file download answered %s: %w", resp.Status, effects.ErrRetryable)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return connector.Response{}, fmt.Errorf("telegram: file download: %w: %w", err, effects.ErrRetryable)
	}
	if len(data) > maxDownload {
		return connector.Response{}, fmt.Errorf("telegram: the file is over a bot's 20 MB: %w", effects.ErrFatal)
	}
	name := strings.TrimSpace(str(req.Input, "name"))
	if name == "" {
		name = "telegram-" + path[strings.LastIndex(path, "/")+1:]
	}
	// What a customer sent: scanned and kept as personal data.
	ref, err := req.Files.Put(ctx, name, data, true)
	if err != nil {
		return connector.Response{}, runfiles.ActionError(err)
	}
	return connector.Response{Output: map[string]any{"file": ref}}, nil
}

// sendSound sends a voice message (sendVoice) or an audio file
// (sendAudio): a run file's reference is uploaded with the request; a
// file_id or URL is passed as it is.
func (c *client) sendSound(method, field string, types []string) func(context.Context, connector.Request) (connector.Response, error) {
	return func(ctx context.Context, req connector.Request) (connector.Response, error) {
		in := req.Input
		id, err := chatID(in, true)
		if err != nil {
			return connector.Response{}, err
		}
		given, file := strings.TrimSpace(str(in, field)), in["file"]
		if (given == "") == (file == nil) {
			return connector.Response{}, fmt.Errorf("telegram: give exactly one of file (a run file) and %s (a file_id or URL): %w", field, effects.ErrFatal)
		}
		body := map[string]any{"chat_id": id}
		copyOptional(body, in, "caption", "parse_mode", "duration", "message_thread_id", "disable_notification", "protect_content")
		if err := replyTo(body, in); err != nil {
			return connector.Response{}, err
		}
		var part *connector.FilePart
		if file != nil {
			if req.Files == nil {
				return connector.Response{}, noFiles()
			}
			data, ref, err := req.Files.Read(ctx, file)
			if err != nil {
				return connector.Response{}, runfiles.ActionError(err)
			}
			ctype, _ := ref["type"].(string)
			ok := false
			for _, t := range types {
				ok = ok || strings.HasPrefix(ctype, t)
			}
			if !ok {
				return connector.Response{}, fmt.Errorf("telegram: %s takes %s, not %s: %w", method, strings.Join(types, ", "), ctype, effects.ErrFatal)
			}
			name, _ := ref["name"].(string)
			part = &connector.FilePart{Field: field, Name: name, Type: ctype, Data: data}
		} else {
			body[field] = given
		}
		var r any
		if err := c.invoke(ctx, req, method, body, part, &r); err != nil {
			return connector.Response{}, err
		}
		return connector.Response{Output: messageOut(r)}, nil
	}
}

var chatActions = map[string]bool{"typing": true, "record_voice": true, "upload_voice": true, "upload_document": true, "upload_photo": true}

func (c *client) sendChatAction(ctx context.Context, req connector.Request) (connector.Response, error) {
	id, err := chatID(req.Input, true)
	if err != nil {
		return connector.Response{}, err
	}
	action := strings.TrimSpace(str(req.Input, "action"))
	if action == "" {
		action = "typing"
	}
	if !chatActions[action] {
		return connector.Response{}, fmt.Errorf("telegram: action must be typing, record_voice, upload_voice, upload_document or upload_photo: %w", effects.ErrFatal)
	}
	body := map[string]any{"chat_id": id, "action": action}
	copyOptional(body, req.Input, "message_thread_id")
	if err := c.call(ctx, req, "sendChatAction", body, nil); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Output: map[string]any{"ok": true}}, nil
}
