package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// The OpenAI-compatible transcription endpoint, built from OpenAI's public
// speech-to-text guide and API reference (developers.openai.com: "Speech to
// text" and "Create transcription", read 8 October 2026):
//
//	POST {base}/v1/audio/transcriptions   multipart/form-data
//	  file             the recording (up to 25 MB)
//	  model            e.g. whisper-1
//	  language         an ISO 639-1 hint, for models that take one
//	  languages[]      hints for gpt-transcribe, which replaces language
//	  response_format  json -> {"text": "...", "languages": [{"code": "fr"}]}
//
// Self-hosted speech servers commonly speak the same API, so BaseURL may
// point at one inside the operator's network.

// DefaultOpenAIModel takes a single language hint and is widely served by
// compatible self-hosted servers.
const DefaultOpenAIModel = "whisper-1"

// DefaultOpenAIURL is OpenAI's API.
const DefaultOpenAIURL = "https://api.openai.com"

// HTTPClient sends requests (the egress guard's client in production).
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// OpenAI transcribes through an OpenAI-compatible endpoint.
type OpenAI struct {
	BaseURL string // DefaultOpenAIURL when empty
	APIKey  string
	Model   string
	HTTP    HTTPClient // nil: http.DefaultClient (tests only)
}

func (o *OpenAI) Name() string { return "openai" }

// Host is the endpoint's host name (the egress allow-list).
func (o *OpenAI) Host() string {
	u, err := url.Parse(o.base())
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (o *OpenAI) base() string {
	if o.BaseURL == "" {
		return DefaultOpenAIURL
	}
	return strings.TrimRight(o.BaseURL, "/")
}

// Transcribe sends the recording and returns its text.
func (o *OpenAI) Transcribe(ctx context.Context, a Audio) (Transcript, error) {
	ext := Extension(a.MimeType)
	if ext == "" {
		return Transcript{}, ErrUnsupported
	}
	model := o.Model
	if model == "" {
		model = DefaultOpenAIModel
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="voice.%s"`, ext))
	base, _, _ := strings.Cut(a.MimeType, ";")
	h.Set("Content-Type", strings.TrimSpace(base))
	fw, err := mw.CreatePart(h)
	if err != nil {
		return Transcript{}, err
	}
	if _, err := fw.Write(a.Data); err != nil {
		return Transcript{}, err
	}
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", "json")
	if a.Language != "" {
		if strings.HasPrefix(model, "gpt-transcribe") {
			_ = mw.WriteField("languages[]", a.Language)
		} else {
			_ = mw.WriteField("language", a.Language)
		}
	}
	if err := mw.Close(); err != nil {
		return Transcript{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base()+"/v1/audio/transcriptions", &body)
	if err != nil {
		return Transcript{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Transcript{}, fmt.Errorf("voice: transcribing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Type string `json:"type"`
				Code any    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		// The provider's message may quote the request; only its kind is kept.
		return Transcript{}, fmt.Errorf("voice: the transcription service answered %s (%s %v)", resp.Status, e.Error.Type, e.Error.Code)
	}
	var out struct {
		Text      string `json:"text"`
		Language  string `json:"language"`
		Languages []struct {
			Code string `json:"code"`
		} `json:"languages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Transcript{}, errors.New("voice: the transcription service answered with something other than JSON")
	}
	t := Transcript{Text: strings.TrimSpace(out.Text), Language: out.Language}
	if len(out.Languages) > 0 {
		t.Language = out.Languages[0].Code
	}
	if t.Text == "" {
		return t, ErrEmpty
	}
	return t, nil
}
