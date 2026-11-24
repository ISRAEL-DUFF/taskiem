package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/forms"
	"github.com/israel-duff/taskiem/engine/pii"
	"github.com/israel-duff/taskiem/engine/wd"
	"github.com/israel-duff/taskiem/engine/whatsapp"
)

// A tool's device inputs in WhatsApp and Telegram (build-plan-gaps H3):
// asked one by one as a tool's other inputs are (tools_chat.go), a
// location answered by the chat's own location message and a photo by a
// photo, as a form step's are (forms_chat.go), each `sent`; a code typed;
// a signature, which a chat cannot draw, sent unavailable.

// toolDeviceAnswer takes a location or a photo message as the answer to
// the tool's input being asked: the location sealed while it waits, under
// the member who sent it; the photo fetched and held through the forms'
// file rules. False when no such input is being asked.
func (s *Server) toolDeviceAnswer(ctx context.Context, c *chat) (bool, error) {
	if c.state != stateCollecting || c.data.Tool == "" || c.data.Flow != "" || (c.in.Location == "" && c.in.Image == nil) || c.p == nil {
		return false, nil
	}
	channel := c.ch.Name()
	if channel != forms.AppWhatsApp && channel != forms.AppTelegram {
		return false, nil
	}
	fields, err := s.waFields(ctx, c)
	if err != nil || c.data.Index >= len(fields) {
		return false, err
	}
	f := fields[c.data.Index]
	data := c.data
	if data.Inputs == nil {
		data.Inputs = map[string]any{}
	}
	switch {
	case f.Device == wd.DeviceLocation && c.in.Location != "":
		lat, lng, ok := latLngOf(c.in.Location)
		if !ok {
			return true, c.say(ctx, s, c.t("wa.field.not_location")+"\n\n"+f.PromptIn(c.lang.Tag))
		}
		var v any
		err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
			paths := pii.LocationsOwnedBy([]pii.Path{{Segments: []string{"lat"}, Category: pii.CategoryLocation}, {Segments: []string{"lng"}, Category: pii.CategoryLocation}},
				&pii.Owner{Category: "member", Value: c.p.UserID.String()})
			var err error
			v, err = pii.Seal(ctx, s.Store.PII, tx, c.tenant.ID, map[string]any{"lat": lat, "lng": lng}, paths, pii.Taint{})
			return err
		})
		if err != nil {
			return true, err
		}
		return true, s.waCollected(ctx, c, data, fields, v)
	case f.Device == wd.DevicePhoto && c.in.Image != nil:
		if s.Forms == nil {
			return false, nil
		}
		d, err := s.waToolDefinition(ctx, c)
		if err != nil {
			return true, err
		}
		props, _ := toolInputsSchema(d)["properties"].(map[string]any)
		prop, _ := props[f.Name].(map[string]any)
		raw, err := s.fetchHeldMedia(ctx, c, channel, c.in.Image)
		if err != nil {
			s.Logger.Warn("tools: fetching a photo sent in chat", "err", err)
			return true, c.say(ctx, s, c.t("wa.field.photo_lost")+"\n\n"+f.PromptIn(c.lang.Tag))
		}
		u := forms.Upload{Meta: forms.FileMeta{Field: f.Name, Name: "photo", Type: http.DetectContentType(raw), Size: int64(len(raw))}, Data: raw}
		ref, why, err := s.Forms.HoldToolFile(ctx, c.tenant.ID, c.p.UserID, data.Tool, f.Name, prop, u, channel, false)
		clear(raw)
		if err != nil {
			return true, err
		}
		if why != "" {
			return true, c.say(ctx, s, c.t("wa.run.does_not_fit", "reason", whatsapp.SafeText(why), "prompt", f.PromptIn(c.lang.Tag)))
		}
		return true, s.waCollected(ctx, c, data, fields, toolFileValue(ref, ""))
	}
	return false, nil
}

// waToolDefinition is the definition of the tool a chat session asks for.
func (s *Server) waToolDefinition(ctx context.Context, c *chat) (*wd.Definition, error) {
	var def []byte
	if err := db0(ctx, s, c.tenant.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT definition FROM workflow_versions WHERE workflow_id = $1 AND version = $2`, c.data.Workflow, c.data.Version).Scan(&def)
	}); err != nil {
		return nil, err
	}
	return s.definitionFor(c.data.Workflow, c.data.Version, def)
}

// chatDeviceInputs fills what a chat did not give a tool's device inputs:
// a signature (no pad in a chat), and any other left out, unavailable.
func chatDeviceInputs(d *wd.Definition, input map[string]any, channel string) {
	where := "WhatsApp"
	if channel == forms.AppTelegram {
		where = "Telegram"
	}
	for name, kind := range wd.DeviceInputs(d) {
		if _, ok := input[name]; ok || kind == wd.DeviceScan {
			continue
		}
		input[name] = map[string]any{"state": "unavailable", "reason": kind + " cannot be given in " + where}
	}
}

// chatInputWords is a chat's inputs as they are shown before a tool runs:
// a location or a photo in words, never a place.
func chatInputWords(inputs map[string]any) map[string]any {
	out := make(map[string]any, len(inputs))
	for k, v := range inputs {
		m, _ := v.(map[string]any)
		switch {
		case m != nil && m["file"] != nil:
			out[k] = "a photo sent"
		case m != nil && m["lat"] != nil:
			out[k] = "a location sent"
		default:
			out[k] = v
		}
	}
	return out
}
