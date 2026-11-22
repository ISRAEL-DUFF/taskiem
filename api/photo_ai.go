package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/ai"
)

// Reading a photo with AI (I3): a phone or a browser reads a photo's text
// itself (I1, I2) and sends only the words. Handwriting it cannot read
// may be sent to the AI model, but only where an owner turned it on: a
// photo cannot be redacted as words are, and an account number is what
// it is read for. The photo is held in memory for the one call and never
// kept; the call is recorded (tokens and outcome, no words) against the
// business's AI budget, and its words go back only to the person who
// asked, to be read as the phone's are (from: "photo").

// maxPhotoBytes is the largest photo read with AI; the apps send it
// already made smaller.
const maxPhotoBytes = 8 << 20

// photoAIPerHour is how many photos one person may have read an hour.
const photoAIPerHour = 30

// photoAIPrompt asks the model for the words only, as written.
const photoAIPrompt = `This is a photo someone took to pay people or save their bank details: a handwritten or printed list, a note, or a screenshot.
Write out every line of text in it exactly as written, one line of the photo a line, top to bottom. On one line, keep the things that are on one line of the photo (a name, an account number, a bank, an amount).
Copy digits exactly, as written; never correct, complete or guess a number. Leave out a word you cannot read.
Answer with the text alone: no title, no notes, no formatting.`

// aiPhotoReadingOn is whether the business lets photos be read with AI.
func aiPhotoReadingOn(ctx context.Context, tx pgx.Tx, p *Principal) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `SELECT ai_photo_reading FROM tenants WHERE id = $1`, p.TenantID).Scan(&on)
	return on, err
}

// getAIPhotoReading is GET /v1/settings/ai-photo-reading: whether it is on
// and whether this deployment can do it.
func (s *Server) getAIPhotoReading(w http.ResponseWriter, r *http.Request) {
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, err = aiPhotoReadingOn(r.Context(), tx, principalFrom(r.Context()))
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ai_photo_reading": on, "available": s.photoModel() != nil})
}

// putAIPhotoReading is PUT /v1/settings/ai-photo-reading {ai_photo_reading}:
// an owner turns it on or off.
func (s *Server) putAIPhotoReading(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"ai_photo_reading"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	p := principalFrom(r.Context())
	if !isOwnerOf(p) {
		s.payFail(w, r, refusePay(http.StatusForbidden, "owner_only", "Only an owner says whether photos may be read with AI."))
		return
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE tenants SET ai_photo_reading = $2 WHERE id = $1`, p.TenantID, req.On); err != nil {
			return err
		}
		return auditTx(r, tx, "settings.ai_photo_reading", p.TenantID.String(), map[string]any{"ai_photo_reading": req.On})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ai_photo_reading": req.On, "available": s.photoModel() != nil})
}

// photoModel is the model photos are read with: the deployment's main
// model (the chat's may be a self-hosted one that cannot see).
func (s *Server) photoModel() ai.Provider {
	if s.AI == nil || s.AI.Provider == nil || s.AI.Provider.Name() == "selfhosted" {
		return nil
	}
	return s.AI.Provider
}

// readPhotoAI is POST /v1/photo-text (multipart, a "photo" part): the text
// in a photo, read with AI, where the business allows it. Nothing of the
// photo is kept.
func (s *Server) readPhotoAI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principalFrom(ctx)
	model := s.photoModel()
	if model == nil {
		s.payFail(w, r, refusePay(http.StatusServiceUnavailable, "unavailable", "Reading photos with AI is not available here."))
		return
	}
	var on bool
	if err := s.tx(r, func(tx pgx.Tx) (err error) {
		on, err = aiPhotoReadingOn(ctx, tx, p)
		return err
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	if !on {
		s.payFail(w, r, refusePay(http.StatusConflict, "off", "Reading photos with AI is off: an owner can turn it on in Settings."))
		return
	}
	if !s.limiter("photo-ai:"+p.Actor(), time.Hour/photoAIPerHour, photoAIPerHour).Allow() {
		s.payFail(w, r, refusePay(http.StatusTooManyRequests, "too_many", "Too many photos read just now: wait a little."))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+64<<10)
	file, _, err := r.FormFile("photo")
	if err != nil {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "no_photo", "Send the photo, up to 8 MB."))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxPhotoBytes {
		s.payFail(w, r, refusePay(http.StatusBadRequest, "no_photo", "Send the photo, up to 8 MB."))
		return
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		s.payFail(w, r, refusePay(http.StatusBadRequest, "not_a_photo", "That is not a photo Taskiem can read (a JPEG, PNG, WebP or GIF)."))
		return
	}
	req := ai.Request{
		System:    []ai.SystemBlock{{Text: photoAIPrompt}},
		Messages:  []ai.Message{{Role: "user", Text: "Write out the text in this photo.", Images: []ai.Image{{MediaType: kind, Data: data}}}},
		MaxTokens: 4000,
		Effort:    "low",
	}
	release, err := s.reserveAI(ctx, p.TenantID, "photo_text", req.Estimate(), chatAIReservation)
	if errors.Is(err, ai.ErrBudgetExhausted) {
		s.payFail(w, r, refusePay(http.StatusConflict, "budget", "This month's AI budget is used up: type the details in."))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer release()
	resp, err := model.Complete(ctx, req)
	outcome, errText := "valid", ""
	switch {
	case err != nil:
		outcome, errText = "error", err.Error()
	case resp.StopReason == ai.StopRefusal:
		outcome = "refused"
	}
	s.recordModelCallFor(ctx, p.TenantID, p.Actor(), "photo_text", "photo_text", model, resp, outcome, errText)
	if err != nil {
		s.Logger.Warn("reading a photo with AI", "err", err)
		s.payFail(w, r, refusePay(http.StatusBadGateway, "ai_down", "The photo could not be read just now: try again, or type the details in."))
		return
	}
	text := ""
	if outcome == "valid" {
		text = strings.TrimSpace(resp.Text)
	}
	if err := s.tx(r, func(tx pgx.Tx) error {
		return auditTx(r, tx, "photo.read_ai", p.Actor(), map[string]any{"bytes": len(data), "lines": strings.Count(text, "\n") + boolInt(text != "")})
	}); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}
