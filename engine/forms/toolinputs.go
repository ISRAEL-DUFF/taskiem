package forms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/wd"
)

// A tool's device inputs (build-plan-gaps H3, docs/spec/device-fields.md
// §17): a members' tool may ask for a location, a photo and a signature
// among its top-level inputs, read as a form step's answers are read. A
// photo or a signature is sent first, on its own (HoldToolFile), scanned,
// cleaned and kept as the forms' files are, held for that person, tool and
// input for a day; the start names it ({file: id}) and takes it once
// (TakeToolFiles), and the run it starts keeps it (AttachToolFiles).

// ToolFileHold is how long a file sent for a tool's input waits for the
// start that takes it.
const ToolFileHold = 24 * time.Hour

// HeldFile is a photo or a signature sent for a tool's input, as a start
// takes it.
type HeldFile struct {
	Ref    FileRef
	Source string
}

// MaxHeldToolFiles is how many files a person may hold for one tool that
// no start has taken yet.
const MaxHeldToolFiles = 10

// ErrTooManyHeld is a file sent for a tool while the person holds
// MaxHeldToolFiles for it that no start has used.
var ErrTooManyHeld = fmt.Errorf("you have sent %d files for this tool that no start has used yet: use them, or send more in a day", MaxHeldToolFiles)

// ErrHeldFile is a start naming a file that is not the person's for this
// tool's input, was taken by another start, or waited too long.
var ErrHeldFile = errors.New("that file was not sent for this input, or was used already: take it again")

// HoldToolFile keeps a photo or a signature user sent for a tool's input:
// the forms' file rules (a photo JPEG or PNG and cleaned; a signature a
// PNG of at most 1 MB; scanned for malware), then held as a submission of
// their own until a start takes it. channel is app (the phone app or the
// web), whatsapp or telegram; phone that the phone app took it with its
// camera. The second value refuses it in plain words.
func (s *Service) HoldToolFile(ctx context.Context, tenant, user uuid.UUID, command, field string, prop map[string]any, u Upload, channel string, phone bool) (FileRef, string, error) {
	kind := wd.DeviceKind(prop)
	if kind != wd.DevicePhoto && kind != wd.DeviceSignature {
		return FileRef{}, fmt.Sprintf("%q takes no file", field), nil
	}
	// A person holds a few files for a tool at once, not a store of them.
	var held int
	if err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM tool_input_files WHERE user_id = $1 AND command = $2 AND taken_at IS NULL AND created_at > now() - $3::interval`,
			user, command, ToolFileHold.String()).Scan(&held)
	}); err != nil {
		return FileRef{}, "", err
	}
	if held >= MaxHeldToolFiles {
		return FileRef{}, "", ErrTooManyHeld
	}
	ref := NewReference()
	u.Meta.Field = field
	tg := target{schema: map[string]any{"type": "object", "properties": map[string]any{field: prop}}}
	_, stored, why, err := s.files(ctx, Incoming{Tenant: tenant, Reference: ref, Uploads: []Upload{u}}, tg)
	if err != nil || why != "" {
		return FileRef{}, why, errors.Join(err, s.discard(ctx, tenant, stored))
	}
	if len(stored) != 1 {
		return FileRef{}, "", errors.New("forms: a tool's file was not stored")
	}
	f := stored[0]
	source := "drawn"
	if kind == wd.DevicePhoto {
		source = "sent"
		if phone && channel == "app" {
			source = "camera"
		}
	}
	if channel != AppWhatsApp && channel != AppTelegram {
		channel = "app"
	}
	answers, _ := json.Marshal(map[string]any{field: map[string]any{"state": "answered", "source": source, "id": f.ref.ID, "name": f.ref.Name, "type": f.ref.Type, "size": f.ref.Size, "sha256": f.ref.SHA256}})
	respondent, _ := json.Marshal(map[string]any{"kind": "member", "member_id": user.String()})
	h := sha256.Sum256([]byte(ref))
	sub := uuid.Must(uuid.NewV7())
	err = db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO form_submissions (id, tenant_id, reference, status_hash, answers, respondent, channel, note, retain_until)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::interval)`,
			sub, tenant, ref, h[:], answers, respondent, channel, "held for the tool "+command, ToolFileHold.String()); err != nil {
			return err
		}
		sum, _ := hex.DecodeString(f.ref.SHA256)
		if _, err := tx.Exec(ctx, `INSERT INTO form_files (id, tenant_id, submission_id, field, name, type, size, sha256, storage_key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			uuid.MustParse(f.ref.ID), tenant, sub, field, f.ref.Name, f.ref.Type, f.ref.Size, sum, f.path); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tool_input_files (id, tenant_id, submission_id, user_id, command, field, kind, source) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			uuid.MustParse(f.ref.ID), tenant, sub, user, command, field, kind, source); err != nil {
			return err
		}
		return audit(ctx, tx, tenant, "form.file_held", ref, map[string]any{"tool": command, "field": field, "kind": kind, "source": source, "channel": channel})
	})
	if err != nil {
		return FileRef{}, "", errors.Join(err, s.discard(ctx, tenant, stored))
	}
	return f.ref, "", nil
}

// TakeToolFiles takes the files a start names (input to file id) for
// user's run of command: each sent by them for that input, not taken by
// another start, and less than a day old. Taken once: release them
// (ReleaseToolFiles) when the start fails.
// ToolStartKey names a start: its trigger and dedup key, as the run's
// trigger receipt has them. The same start sent again takes its files
// again (and is answered with its run).
type ToolStartKey struct {
	Trigger, Dedup string
}

func (s *Service) TakeToolFiles(ctx context.Context, tenant, user uuid.UUID, command string, want map[string]string, start ToolStartKey) (map[string]HeldFile, error) {
	out := map[string]HeldFile{}
	if len(want) == 0 {
		return out, nil
	}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		for field, id := range want {
			fid, err := uuid.Parse(id)
			if err != nil {
				return fmt.Errorf("%w (%s)", ErrHeldFile, field)
			}
			var h HeldFile
			var sum []byte
			// Untaken, or taken by this same start (sent again).
			err = tx.QueryRow(ctx, `UPDATE tool_input_files t SET taken_at = now(), taken_trigger = $6, taken_dedup = $7 FROM form_files f
				WHERE t.id = $1 AND f.id = t.id AND t.user_id = $2 AND t.command = $3 AND t.field = $4
				  AND (t.taken_at IS NULL OR (t.taken_trigger = $6 AND t.taken_dedup = $7))
				  AND t.created_at > now() - $5::interval
				RETURNING t.source, f.name, f.type, f.size, f.sha256`, fid, user, command, field, ToolFileHold.String(), start.Trigger, start.Dedup).
				Scan(&h.Source, &h.Ref.Name, &h.Ref.Type, &h.Ref.Size, &sum)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w (%s)", ErrHeldFile, field)
			}
			if err != nil {
				return err
			}
			h.Ref.ID, h.Ref.SHA256 = fid.String(), hex.EncodeToString(sum)
			out[field] = h
			// Taken: kept as long as a run keeps its data, should its run
			// not be set on it at once (AttachTakenToolFiles sets it later).
			if _, err := tx.Exec(ctx, `UPDATE form_submissions SET retain_until = GREATEST(retain_until, now() + interval '90 days') WHERE id = (SELECT submission_id FROM tool_input_files WHERE id = $1) AND run_id IS NULL`, fid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseToolFiles gives taken files back, for a start that failed.
func (s *Service) ReleaseToolFiles(ctx context.Context, tenant uuid.UUID, files map[string]HeldFile) error {
	ids := heldIDs(files)
	if len(ids) == 0 {
		return nil
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE tool_input_files SET taken_at = NULL, taken_trigger = NULL, taken_dedup = NULL WHERE id = ANY ($1)`, ids); err != nil {
			return err
		}
		// Held again for its day, from when it was sent.
		_, err := tx.Exec(ctx, `UPDATE form_submissions s SET retain_until = t.created_at + $2::interval FROM tool_input_files t
			WHERE t.submission_id = s.id AND t.id = ANY ($1) AND s.run_id IS NULL`, ids, ToolFileHold.String())
		return err
	})
}

// AttachTakenToolFiles gives every file a start took to the run that
// start made (its trigger receipt), where that was not done at once: a
// failure after the run started never leaves its files to expire.
func (s *Service) AttachTakenToolFiles(ctx context.Context, tenant uuid.UUID) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE form_submissions s SET run_id = r.run_id, status = 'started', note = NULL, retain_until = NULL, updated_at = now()
			FROM tool_input_files t JOIN trigger_receipts r ON r.tenant_id = t.tenant_id AND r.trigger_id = t.taken_trigger AND r.dedup_key = t.taken_dedup
			WHERE t.submission_id = s.id AND t.taken_at IS NOT NULL AND s.run_id IS NULL AND r.run_id IS NOT NULL`)
		return err
	})
}

// AttachToolFiles gives taken files to the run that took them: they go
// with its data when retention purges it.
func (s *Service) AttachToolFiles(ctx context.Context, tenant uuid.UUID, files map[string]HeldFile, run uuid.UUID) error {
	ids := heldIDs(files)
	if len(ids) == 0 {
		return nil
	}
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE form_submissions SET run_id = $2, status = 'started', note = NULL, retain_until = NULL, updated_at = now()
			WHERE id IN (SELECT submission_id FROM tool_input_files WHERE id = ANY ($1))`, ids, run)
		return err
	})
}

func heldIDs(files map[string]HeldFile) []uuid.UUID {
	var ids []uuid.UUID
	for _, f := range files {
		if id, err := uuid.Parse(f.Ref.ID); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ToolFileIDs are the files a start's input names for its photo and
// signature inputs: {file: id}.
func ToolFileIDs(schema map[string]any, input map[string]any) map[string]string {
	out := map[string]string{}
	for name, kind := range wd.DeviceFields(schema) {
		if kind != wd.DevicePhoto && kind != wd.DeviceSignature {
			continue
		}
		if m, ok := input[name].(map[string]any); ok {
			if id, ok := m["file"].(string); ok && id != "" {
				out[name] = id
			}
		}
	}
	return out
}

// ReadToolInputs reads a tool start's location, photo and signature
// inputs into what the run keeps, as a form step's answers are read
// (deviceAnswer): each with its state, and a source from the channel
// (app: the phone app or the web, whatsapp, telegram), never from the
// input; a photo and a signature only as the files taken for this start.
// A scan is left to the run (wd.ReadScanInput). The second value refuses
// the input in plain words.
func ReadToolInputs(schema map[string]any, input map[string]any, channel string, files map[string]HeldFile, now time.Time) string {
	if channel != AppWhatsApp && channel != AppTelegram && channel != ChannelAPI {
		channel = "app"
	}
	sub := Submission{Channel: channel}
	props, _ := schema["properties"].(map[string]any)
	for _, name := range wd.SchemaOrder(schema) {
		p, _ := props[name].(map[string]any)
		kind := wd.DeviceKind(p)
		if kind == "" || kind == wd.DeviceScan {
			continue
		}
		v, present := input[name]
		if !present || v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Sprintf("%q: the answer is not one %s input takes", name, kind)
		}
		uploaded := false
		if kind == wd.DevicePhoto || kind == wd.DeviceSignature {
			if _, named := m["file"]; named {
				h, ok := files[name]
				if !ok {
					return fmt.Sprintf("%q: %s", name, ErrHeldFile.Error())
				}
				fm := map[string]any{"id": h.Ref.ID, "name": h.Ref.Name, "type": h.Ref.Type, "size": h.Ref.Size, "sha256": h.Ref.SHA256}
				if who, ok := m["signed_by"].(string); ok {
					fm["signed_by"] = who
				}
				m, uploaded = fm, true
			}
		}
		out, why := deviceAnswer(name, kind, p, m, sub, now, uploaded)
		if why != "" {
			return why
		}
		if h, ok := files[name]; ok && uploaded && out["source"] != nil {
			// How the file was got, as it was sent.
			out["source"] = h.Source
			if want, _ := p["require_source"].(string); want != "" && h.Source != want {
				out["state"] = "unverified"
			} else {
				out["state"] = "answered"
			}
		}
		input[name] = out
	}
	return ""
}
