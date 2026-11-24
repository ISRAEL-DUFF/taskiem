package speech

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/runfiles/runfilestest"
	"github.com/israel-duff/taskiem/engine/speech"
	"github.com/israel-duff/taskiem/engine/voice"
)

type fakeOps struct {
	tenant uuid.UUID
	heard  speech.Heard
	err    error
	mime   string
	lang   string
}

func (f *fakeOps) Transcribe(_ context.Context, tenant uuid.UUID, _ []byte, mime, language string) (speech.Heard, error) {
	f.tenant, f.mime, f.lang = tenant, mime, language
	return f.heard, f.err
}

func (f *fakeOps) Speak(_ context.Context, tenant uuid.UUID, text, _, format, _ string) (speech.Spoken, error) {
	f.tenant = tenant
	if f.err != nil {
		return speech.Spoken{}, f.err
	}
	if format == "mp3" {
		return speech.Spoken{Data: []byte("ID3 mp3"), Type: "audio/mpeg", Seconds: 1, Characters: len(text)}, nil
	}
	return speech.Spoken{Data: voice.TestOgg(2, 8), Type: "audio/ogg", Seconds: 2, Characters: len(text)}, nil
}

func (f *fakeOps) CannotHear(reason, language string) string { return reason + "/" + language }

var tenant = uuid.MustParse("01930000-0000-7000-8000-000000000001")

func call(c *connector.Connector, action string, in map[string]any, files connector.Files) (map[string]any, error) {
	ctx := connector.WithCaller(context.Background(), connector.Caller{Tenant: tenant})
	r, err := c.Actions[action].Execute(ctx, connector.Request{Input: in, Files: files, Attempt: 1})
	out, _ := r.Output.(map[string]any)
	return out, err
}

func TestTranscribeReadsTheRunFile(t *testing.T) {
	ops := &fakeOps{heard: speech.Heard{OK: true, Text: "Do you have rice?", Language: "en", Seconds: 3}}
	fs := runfilestest.NewFiles()
	ref, _ := fs.Put(context.Background(), "voice.ogg", voice.TestOgg(3, 8), true)
	out, err := call(New(ops), "transcribe", map[string]any{"file": ref, "language": "pcm"}, fs)
	if err != nil || out["heard"] != true || out["text"] != "Do you have rice?" || out["seconds"] != int64(3) {
		t.Fatalf("%v %v", out, err)
	}
	if ops.tenant != tenant || ops.mime != "audio/ogg" || ops.lang != "pcm" {
		t.Fatalf("asked for %v %s %s", ops.tenant, ops.mime, ops.lang)
	}
	// Not heard: the reason and the fixed answer, and no failure.
	ops.heard = speech.Heard{Reason: speech.ReasonTooLong, Reply: "too long"}
	if out, err := call(New(ops), "transcribe", map[string]any{"file": ref}, fs); err != nil || out["heard"] != false || out["reply"] != "too long" || out["text"] != nil {
		t.Fatalf("not heard: %v %v", out, err)
	}
}

func TestErrorsAreSorted(t *testing.T) {
	fs := runfilestest.NewFiles()
	ref, _ := fs.Put(context.Background(), "voice.ogg", voice.TestOgg(1, 8), true)
	for err, want := range map[error]error{
		fmt.Errorf("%w: 503", speech.ErrService): effects.ErrRetryable,
		speech.ErrOff:                            effects.ErrFatal,
		speech.ErrOverAllowance:                  effects.ErrFatal,
		speech.ErrUnavailable:                    effects.ErrFatal,
		speech.ErrRefused:                        effects.ErrFatal,
	} {
		ops := &fakeOps{err: err}
		if _, got := call(New(ops), "transcribe", map[string]any{"file": ref}, fs); !errors.Is(got, want) || !errors.Is(got, err) {
			t.Errorf("%v: got %v", err, got)
		}
		if _, got := call(New(ops), "speak", map[string]any{"text": "Yes"}, fs); !errors.Is(got, want) {
			t.Errorf("speak %v: got %v", err, got)
		}
	}
	// No run, no service, no file store, or an unknown file: fatal.
	if _, err := New(&fakeOps{}).Actions["speak"].Execute(context.Background(), connector.Request{Input: map[string]any{"text": "x"}, Files: fs}); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("outside a run: %v", err)
	}
	if _, err := call(New(nil), "speak", map[string]any{"text": "x"}, fs); !errors.Is(err, effects.ErrFatal) || !errors.Is(err, speech.ErrUnavailable) {
		t.Fatalf("no service: %v", err)
	}
	if _, err := call(New(&fakeOps{}), "speak", map[string]any{"text": "x"}, nil); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("no file store: %v", err)
	}
	if _, err := call(New(&fakeOps{}), "transcribe", map[string]any{"file": map[string]any{"file": "nope"}}, fs); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("unknown file: %v", err)
	}
}

func TestSpeakKeepsTheReply(t *testing.T) {
	fs := runfilestest.NewFiles()
	out, err := call(New(&fakeOps{}), "speak", map[string]any{"text": "We have rice."}, fs)
	if err != nil {
		t.Fatal(err)
	}
	ref := out["file"].(map[string]any)
	data, personal := fs.Kept(ref["file"].(string))
	if ref["name"] != "reply.ogg" || personal || len(data) == 0 || out["seconds"] != int64(2) || out["characters"] != 13 {
		t.Fatalf("%v personal %v", out, personal)
	}
	if out, err := call(New(&fakeOps{}), "speak", map[string]any{"text": "Yes", "format": "mp3"}, fs); err != nil || out["file"].(map[string]any)["name"] != "reply.mp3" {
		t.Fatalf("mp3: %v %v", out, err)
	}
	// Tied to the customer it answers: a number, or a Telegram chat.
	for kind, cat := range map[string]string{"": "phone", "phone": "phone", "chat": "other"} {
		out, err := call(New(&fakeOps{}), "speak", map[string]any{"text": "Yes", "customer": "42", "customer_kind": kind}, fs)
		if err != nil {
			t.Fatal(err)
		}
		if who := fs.About(out["file"].(map[string]any)["file"].(string)); who.Category != cat || who.Value != "42" {
			t.Errorf("%q: about %v", kind, who)
		}
	}
}

func TestCannotHearNeedsNoService(t *testing.T) {
	out, err := call(New(&fakeOps{}), "cannot_hear", map[string]any{"reason": "unavailable", "language": "yo"}, nil)
	if err != nil || out["reply"] != "unavailable/yo" {
		t.Fatalf("%v %v", out, err)
	}
	out, err = call(New(nil), "cannot_hear", map[string]any{"reason": "unavailable", "language": "pcm"}, nil)
	if err != nil || out["reply"] == "" {
		t.Fatalf("without a service: %v %v", out, err)
	}
	var late Late
	if r := late.CannotHear("empty", "en"); r == "" {
		t.Fatal("Late before Set")
	}
}
