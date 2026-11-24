package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A tool's device inputs (build-plan-gaps H3, docs/spec/device-fields.md
// §17): a members' tool may ask for a location, a photo and a signature
// among its top-level inputs. The app and the web send a photo or a
// signature first (POST /v1/tools/{command}/files), then start the tool
// naming it ({file: id}); a location is sent as the device read it. The
// start reads them as a form step's answers are read: a source from the
// channel, never the body's, and a file only as the one this person sent
// for this tool's input, taken once. Care is unchanged: a tool that asks
// shows what will run first, with each device input in words.

// toolInputsSchema is a tool's inputs schema, as a map.
func toolInputsSchema(d *wd.Definition) map[string]any {
	raw, _ := d.InputsSchema()
	schema, _ := raw.(map[string]any)
	return schema
}

// toolDefinition is the tool's deployed definition.
func (s *Server) toolDefinition(ctx context.Context, tenant uuid.UUID, t toolRow) (*wd.Definition, error) {
	var def []byte
	if err := db0(ctx, s, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, t.Workflow, t.Version).Scan(&def)
	}); err != nil {
		return nil, err
	}
	return s.definitionFor(t.Workflow, t.Version, def)
}

// readToolDevices reads a start's device inputs for run: the files it
// names taken (given back by release when the start fails), each input
// as the run keeps it. A start with no device inputs is returned as it is.
func (s *Server) readToolDevices(ctx context.Context, p *Principal, t toolRow, input map[string]any, channel string) (map[string]any, map[string]forms.HeldFile, error) {
	d, err := s.toolDefinition(ctx, p.TenantID, t)
	if err != nil {
		return nil, nil, err
	}
	devices := wd.DeviceInputs(d)
	if len(devices) == 0 {
		return input, nil, nil
	}
	schema := toolInputsSchema(d)
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	var files map[string]forms.HeldFile
	if ids := forms.ToolFileIDs(schema, out); len(ids) > 0 {
		if s.Forms == nil {
			return nil, nil, toolInputError{"files cannot be taken here"}
		}
		if files, err = s.Forms.TakeToolFiles(ctx, p.TenantID, p.UserID, t.Command, ids); errors.Is(err, forms.ErrHeldFile) {
			return nil, nil, toolInputError{err.Error()}
		} else if err != nil {
			return nil, nil, err
		}
	}
	if why := forms.ReadToolInputs(schema, out, channel, files, time.Now()); why != "" {
		if s.Forms != nil {
			if err := s.Forms.ReleaseToolFiles(ctx, p.TenantID, files); err != nil {
				return nil, nil, err
			}
		}
		return nil, nil, toolInputError{why}
	}
	return out, files, nil
}

// deviceInputWords is a device input in words, for what a tool will run
// with and its echo: never a place, a code's text only.
func deviceInputWords(kind string, v any) string {
	m, _ := v.(map[string]any)
	if st, _ := m["state"].(string); st == "refused" || st == "unavailable" {
		return "not given"
	}
	switch kind {
	case wd.DeviceLocation:
		switch {
		case m["address"] != nil:
			return "an address typed"
		case m["source"] == "sent":
			return "a location sent"
		case m["source"] == "device":
			return "your location, read now"
		}
		return "a location"
	case wd.DevicePhoto:
		if m["source"] == "camera" {
			return "a photo taken in the app"
		}
		return "a photo"
	case wd.DeviceSignature:
		if who, ok := m["signed_by"].(string); ok && who != "" {
			return "signed by " + who
		}
		return "signed"
	}
	return ""
}

// holdToolFileRoute is POST /v1/tools/{command}/files: a photo or a
// signature for one of the tool's inputs, a multipart form with the file
// as the part named by its input (and app: "app" from the phone app, whose
// photo is taken with its camera). 201 with the file to name in the start
// ({file: id}); 422 when it is not one the input takes.
func (s *Server) holdToolFileRoute(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if s.Forms == nil || p.UserID == uuid.Nil {
		writeErr(w, http.StatusNotFound, "files cannot be taken here")
		return
	}
	env, err := environment(p, r.URL.Query().Get("environment"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var t toolRow
	if err := s.tx(r, func(tx pgx.Tx) error {
		t, err = s.toolFor(r.Context(), tx, p, env, chi.URLParam(r, "command"))
		return err
	}); err != nil {
		s.toolRefused(w, r, err, chi.URLParam(r, "command"))
		return
	}
	d, err := s.toolDefinition(r.Context(), p.TenantID, t)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 26<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // the body is bounded by MaxBytesReader above
		s.fail(w, r, fmt.Errorf("%w: send the file as a multipart form, as the part named by its input", errBadRequest))
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if n := len(r.MultipartForm.File); n != 1 {
		s.fail(w, r, fmt.Errorf("%w: one file at a time, as the part named by its input", errBadRequest))
		return
	}
	devices := wd.DeviceInputs(d)
	props, _ := toolInputsSchema(d)["properties"].(map[string]any)
	for field, hs := range r.MultipartForm.File {
		kind := devices[field]
		if (kind != wd.DevicePhoto && kind != wd.DeviceSignature) || len(hs) != 1 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the values do not fit the tool's form", "problems": []string{fmt.Sprintf("%q takes no file", field)}})
			return
		}
		f, err := hs[0].Open()
		if err != nil {
			s.fail(w, r, fmt.Errorf("%w: the file could not be read", errBadRequest))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, 25<<20+1))
		_ = f.Close()
		if err != nil || len(data) == 0 || len(data) > 25<<20 {
			s.fail(w, r, fmt.Errorf("%w: the file could not be read, or is larger than 25 MB", errBadRequest))
			return
		}
		prop, _ := props[field].(map[string]any)
		u := forms.Upload{Meta: forms.FileMeta{Field: field, Name: hs[0].Filename, Type: http.DetectContentType(data), Size: int64(len(data))}, Data: data}
		ref, why, err := s.Forms.HoldToolFile(r.Context(), p.TenantID, p.UserID, t.Command, field, prop, u, "app", strings.TrimSpace(r.FormValue("app")) == "app")
		clear(data)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if why != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the values do not fit the tool's form", "problems": []string{why}})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"field": field, "file": ref})
	}
}

// toolFileValue is the start's value for a held file: {file: id}, and a
// signature's signer beside it.
func toolFileValue(ref forms.FileRef, signedBy string) map[string]any {
	out := map[string]any{"file": ref.ID}
	if signedBy != "" {
		out["signed_by"] = signedBy
	}
	return out
}
