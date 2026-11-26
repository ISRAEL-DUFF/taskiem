package api

// File previews in each kind of chat (docs/spec/chat-previews-and-editing.md
// §1.3): the picture of a PDF's first page, drawn after the file passed
// its checks, in the background once a message is sent, or the first
// time someone asks for an older one; GET …/files/{id}?preview=1 answers
// it on each chat's file route, and ?view=1 the PDF itself to read inside
// Taskiem. The picture is kept as each chat keeps its files: in each
// business's own storage, sealed in people chats and groups, in the
// person's own rows of their chat with a business.

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/media"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// pdfRenderer draws PDFs' first pages for the whole process: PDFium is
// compiled once, and at most two are drawn at a time.
var pdfRenderer media.PDFRenderer

// pdfFlight draws one copy's picture once when it is asked for twice at
// the same time.
var pdfFlight singleflight.Group

// pdfDrawn is what drawing a PDF learnt: its facts, and its picture.
type pdfDrawn struct {
	facts media.Facts
	webp  []byte
}

// drawPDF draws a PDF's first page. A protected PDF has none (preview
// "none"); one that cannot be drawn in time or memory has failed.
func drawPDF(ctx context.Context, data []byte) pdfDrawn {
	p, err := pdfRenderer.Render(ctx, data)
	switch {
	case err == nil:
		return pdfDrawn{facts: media.Facts{Pages: p.Pages, Preview: "ready", W: p.W, H: p.H}, webp: p.WebP}
	case errors.Is(err, media.ErrPDFProtected):
		return pdfDrawn{facts: media.Facts{Protected: true, Preview: "none"}}
	}
	return pdfDrawn{facts: media.Facts{Preview: "failed"}}
}

// pdfCopy is one copy of a message's file, as its chat keeps it.
type pdfCopy struct {
	// key names the copy, so two asking at once draw it once.
	key string
	// picture answers the file's type and facts, and its picture when
	// one is kept.
	picture func(ctx context.Context) (ctype string, f media.Facts, pic []byte, err error)
	// file answers the file itself.
	file func(ctx context.Context) ([]byte, error)
	// keep keeps what drawing learnt, and the picture.
	keep func(ctx context.Context, d pdfDrawn) error
}

func isPDF(ctype string) bool {
	t, _, _ := mime.ParseMediaType(ctype)
	return t == "application/pdf"
}

// wantsPreview is ?preview=1: the picture of the file, not the file.
func wantsPreview(r *http.Request) bool { return r.URL.Query().Get("preview") == "1" }

// servePDFPreview answers the picture of a copy's PDF: the one kept, or
// drawn now for an older PDF that has none yet (then kept); 404 for a
// file that is no PDF, or a PDF with no picture (protected, failed).
func (s *Server) servePDFPreview(w http.ResponseWriter, r *http.Request, c pdfCopy, fail func(http.ResponseWriter, *http.Request, error)) {
	ctx := r.Context()
	ctype, f, pic, err := c.picture(ctx)
	if err != nil {
		fail(w, r, err)
		return
	}
	if pic == nil && isPDF(ctype) && (f.Preview == "" || f.Preview == "ready") {
		v, err, _ := pdfFlight.Do(c.key, func() (any, error) {
			data, err := c.file(ctx)
			if err != nil {
				return nil, err
			}
			d := drawPDF(context.WithoutCancel(ctx), data)
			if err := c.keep(context.WithoutCancel(ctx), d); err != nil {
				s.Logger.Warn("file preview: not kept", "err", err)
			}
			return d.webp, nil
		})
		if err != nil {
			fail(w, r, err)
			return
		}
		pic, _ = v.([]byte)
	}
	if pic == nil {
		writeErr(w, http.StatusNotFound, "This file has no picture.")
		return
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(pic)))
	_, _ = w.Write(pic)
}

// drawLater draws a just-sent PDF's first page in the background, once,
// and keeps it in each copy, so sending is not slowed: each copy's
// message reads as changed, and the live streams and the app's sync carry
// its picture's state.
func (s *Server) drawLater(ctx context.Context, ctype string, data []byte, copies ...pdfCopy) {
	if !isPDF(ctype) || len(data) == 0 || len(copies) == 0 {
		return
	}
	s.background(ctx, "pdf preview", func(ctx context.Context) {
		d := drawPDF(ctx, data)
		for _, c := range copies {
			if err := c.keep(ctx, d); err != nil {
				s.Logger.Warn("file preview: not kept", "copy", c.key, "err", err)
			}
		}
	})
}

// serveChatFileFor writes a conversation's file as serveChatFile does,
// or, for a PDF asked for with ?view=1, to be read inside Taskiem's viewer:
// inline, in a sandbox with nothing allowed, never sniffed, and only to
// Taskiem's own pages. Every other file stays as it is (a document is
// always downloaded).
func serveChatFileFor(w http.ResponseWriter, r *http.Request, data []byte, ctype, name, kind string) {
	if r.URL.Query().Get("view") == "1" && isPDF(ctype) {
		// Shown in a frame of Taskiem's own page, and nowhere else.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; frame-ancestors 'self'")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		kind = "view"
	}
	serveChatFile(w, data, ctype, name, kind)
}

// --- each chat's copies ---

// linkPDFCopy is a business's copy of a business-to-business message's
// file, in its own storage.
func (s *Server) linkPDFCopy(tenant, link, pair uuid.UUID) pdfCopy {
	attachment := func(ctx context.Context) (links.Attachment, error) {
		var a links.Attachment
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			var err error
			a, err = links.AttachmentTx(ctx, tx, tenant, link, pair)
			return err
		})
		return a, err
	}
	return pdfCopy{
		key: "link:" + tenant.String() + ":" + pair.String(),
		picture: func(ctx context.Context) (string, media.Facts, []byte, error) {
			a, err := attachment(ctx)
			if err != nil || s.Media == nil || a.PreviewPath == "" {
				return a.Type, a.Facts, nil, err
			}
			pic, _, err := s.Media.GetFile(ctx, tenant, a.PreviewPath)
			return a.Type, a.Facts, pic, err
		},
		file: func(ctx context.Context) ([]byte, error) {
			a, err := attachment(ctx)
			if err != nil {
				return nil, err
			}
			if s.Media == nil {
				return nil, pgx.ErrNoRows
			}
			data, _, err := s.Media.GetFile(ctx, tenant, a.Path)
			return data, err
		},
		keep: func(ctx context.Context, d pdfDrawn) error {
			path := ""
			if d.webp != nil && s.Media != nil {
				var err error
				if path, err = s.Media.PutPDFPreview(ctx, tenant, d.webp); err != nil {
					return err
				}
			}
			return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				return links.SetFileFactsTx(ctx, tx, tenant, link, pair, d.facts, path)
			})
		},
	}
}

// customerPDFCopy is a business's copy of a file in its chat with a
// person in the app, in its own storage.
func (s *Server) customerPDFCopy(tenant, msg uuid.UUID) pdfCopy {
	type kept struct {
		Type        string `json:"type"`
		Path        string `json:"path"`
		PreviewPath string `json:"preview_path"`
	}
	attachment := func(ctx context.Context) (kept, media.Facts, error) {
		var raw []byte
		err := db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT attachment FROM customer_messages WHERE tenant_id = $1 AND id = $2 AND attachment ? 'path'`, tenant, msg).Scan(&raw)
		})
		var k kept
		var m map[string]any
		if err == nil {
			_ = json.Unmarshal(raw, &k)
			_ = json.Unmarshal(raw, &m)
		}
		return k, media.FactsOf(m), err
	}
	return pdfCopy{
		key: "customer:" + tenant.String() + ":" + msg.String(),
		picture: func(ctx context.Context) (string, media.Facts, []byte, error) {
			k, f, err := attachment(ctx)
			if err != nil || s.Media == nil || k.PreviewPath == "" {
				return k.Type, f, nil, err
			}
			pic, _, err := s.Media.GetFile(ctx, tenant, k.PreviewPath)
			return k.Type, f, pic, err
		},
		file: func(ctx context.Context) ([]byte, error) {
			k, _, err := attachment(ctx)
			if err != nil {
				return nil, err
			}
			if s.Media == nil {
				return nil, pgx.ErrNoRows
			}
			data, _, err := s.Media.GetFile(ctx, tenant, k.Path)
			return data, err
		},
		keep: func(ctx context.Context, d pdfDrawn) error {
			set := d.facts.Map(nil)
			if d.webp != nil && s.Media != nil {
				path, err := s.Media.PutPDFPreview(ctx, tenant, d.webp)
				if err != nil {
					return err
				}
				set["preview_path"] = path
			}
			raw, _ := json.Marshal(set)
			return db.InTenantTx(ctx, s.Store.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE customer_messages SET attachment = attachment || $3::jsonb WHERE tenant_id = $1 AND id = $2 AND attachment ? 'path'`,
					tenant, msg, raw)
				return err
			})
		},
	}
}

// personPDFCopy is a person's own copy of a file in their chat with a
// business; chat, when given, is the conversation it must be in.
func (s *Server) personPDFCopy(person, msg uuid.UUID, chat *uuid.UUID) pdfCopy {
	return pdfCopy{
		key: "person:" + person.String() + ":" + msg.String(),
		picture: func(ctx context.Context) (ctype string, f media.Facts, pic []byte, err error) {
			err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				var m map[string]any
				if err := tx.QueryRow(ctx, `SELECT m.attachment, (SELECT f.data FROM person_files f WHERE f.person_id = m.person_id AND f.message_id = m.id AND f.kind = 'preview')
					FROM person_messages m WHERE m.person_id = $1 AND m.id = $2 AND ($3::uuid IS NULL OR m.conversation_id = $3) AND m.attachment IS NOT NULL`,
					person, msg, chat).Scan(&m, &pic); err != nil {
					return err
				}
				ctype, _ = m["type"].(string)
				f = media.FactsOf(m)
				return nil
			})
			return ctype, f, pic, err
		},
		file: func(ctx context.Context) ([]byte, error) {
			var data []byte
			err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT data FROM person_files WHERE person_id = $1 AND message_id = $2 AND kind = 'file'`, person, msg).Scan(&data)
			})
			return data, err
		},
		keep: func(ctx context.Context, d pdfDrawn) error {
			raw, _ := json.Marshal(d.facts.Map(nil))
			return db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				if d.webp != nil {
					if _, err := tx.Exec(ctx, `INSERT INTO person_files (id, person_id, tenant_id, message_id, kind, type, size, data)
						SELECT $1, person_id, tenant_id, id, 'preview', 'image/webp', $4, $5 FROM person_messages WHERE person_id = $2 AND id = $3
						ON CONFLICT (message_id) WHERE kind = 'preview' DO NOTHING`, uuid.Must(uuid.NewV7()), person, msg, len(d.webp), d.webp); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `UPDATE person_messages SET attachment = attachment || $3::jsonb WHERE person_id = $1 AND id = $2 AND attachment IS NOT NULL`,
					person, msg, raw)
				return err
			})
		},
	}
}

// previewRow seals the picture of a message's PDF.
func previewRow(id uuid.UUID) string { return "pv:" + id.String() }

// groupPreviewRow seals the picture of a group message's PDF.
func groupPreviewRow(id uuid.UUID) string { return "gpv:" + id.String() }

// peoplePDFCopy is a person's own copy of a file in a people chat, sealed
// under their key; chat, when given, is the chat it must be in.
func (s *Server) peoplePDFCopy(person, msg uuid.UUID, chat *uuid.UUID) pdfCopy {
	return pdfCopy{
		key: "people:" + person.String() + ":" + msg.String(),
		picture: func(ctx context.Context) (ctype string, f media.Facts, pic []byte, err error) {
			err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				var m map[string]any
				var sealed []byte
				if err := tx.QueryRow(ctx, `SELECT m.attachment, (SELECT f.sealed FROM people_files f WHERE f.person_id = m.person_id AND f.message_id = m.id AND f.kind = 'preview')
					FROM people_messages m WHERE m.person_id = $1 AND m.id = $2 AND ($3::uuid IS NULL OR m.chat_id = $3) AND m.deleted_at IS NULL AND m.attachment IS NOT NULL`,
					person, msg, chat).Scan(&m, &sealed); err != nil {
					return err
				}
				ctype, _ = m["type"].(string)
				f = media.FactsOf(m)
				if sealed == nil {
					return nil
				}
				key, err := s.keys().KeyTx(ctx, tx, person, false)
				if err != nil {
					return err
				}
				pic, err = peoplechats.Open(key, person, previewRow(msg), sealed)
				return err
			})
			return ctype, f, pic, err
		},
		file: func(ctx context.Context) ([]byte, error) {
			var data []byte
			err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				var sealed []byte
				if err := tx.QueryRow(ctx, `SELECT sealed FROM people_files WHERE person_id = $1 AND message_id = $2 AND kind = 'file'`, person, msg).Scan(&sealed); err != nil {
					return err
				}
				key, err := s.keys().KeyTx(ctx, tx, person, false)
				if err != nil {
					return err
				}
				data, err = peoplechats.Open(key, person, fileRow(msg), sealed)
				return err
			})
			return data, err
		},
		keep: func(ctx context.Context, d pdfDrawn) error {
			raw, _ := json.Marshal(d.facts.Map(nil))
			return db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				if d.webp != nil {
					key, err := s.keys().KeyTx(ctx, tx, person, false)
					if err != nil {
						return err
					}
					sealed, err := peoplechats.Seal(key, person, previewRow(msg), d.webp)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `INSERT INTO people_files (person_id, message_id, conversation_id, kind, type, size, sealed)
						SELECT person_id, id, conversation_id, 'preview', 'image/webp', $3, $4 FROM people_messages WHERE person_id = $1 AND id = $2 AND deleted_at IS NULL
						ON CONFLICT DO NOTHING`, person, msg, len(d.webp), sealed); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `UPDATE people_messages SET attachment = attachment || $3::jsonb WHERE person_id = $1 AND id = $2 AND attachment IS NOT NULL AND deleted_at IS NULL`,
					person, msg, raw)
				return err
			})
		},
	}
}

// groupPDFCopy is a group's copy of a file, sealed under the group's
// key, as person (a member who sees it) reads and keeps it.
func (s *Server) groupPDFCopy(person, group, msg uuid.UUID) pdfCopy {
	return pdfCopy{
		key: "group:" + group.String() + ":" + msg.String(),
		picture: func(ctx context.Context) (ctype string, f media.Facts, pic []byte, err error) {
			err = db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				m, err := groupMessageTx(ctx, tx, person, group, msg.String())
				if err != nil {
					return err
				}
				if m.deletedAt != nil || m.attach == nil {
					return errDoorNone
				}
				ctype, _ = m.attach["type"].(string)
				f = media.FactsOf(m.attach)
				var sealed []byte
				err = tx.QueryRow(ctx, `SELECT sealed FROM group_files WHERE group_id = $1 AND message_id = $2 AND kind = 'preview'`, group, msg).Scan(&sealed)
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				key, err := s.keys().GroupKeyTx(ctx, tx, group)
				if err != nil {
					return err
				}
				pic, err = peoplechats.Open(key, group, groupPreviewRow(msg), sealed)
				return err
			})
			return ctype, f, pic, err
		},
		file: func(ctx context.Context) ([]byte, error) {
			var data []byte
			err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				var sealed []byte
				if err := tx.QueryRow(ctx, `SELECT sealed FROM group_files WHERE group_id = $1 AND message_id = $2 AND kind = 'file'`, group, msg).Scan(&sealed); err != nil {
					return err
				}
				key, err := s.keys().GroupKeyTx(ctx, tx, group)
				if err != nil {
					return err
				}
				data, err = peoplechats.Open(key, group, groupFileRow(msg), sealed)
				return err
			})
			return data, err
		},
		keep: func(ctx context.Context, d pdfDrawn) error {
			raw, _ := json.Marshal(d.facts.Map(nil))
			return db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
				if d.webp != nil {
					key, err := s.keys().GroupKeyTx(ctx, tx, group)
					if err != nil {
						return err
					}
					sealed, err := peoplechats.Seal(key, group, groupPreviewRow(msg), d.webp)
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `INSERT INTO group_files (group_id, message_id, kind, type, size, sealed) VALUES ($1, $2, 'preview', 'image/webp', $3, $4)
						ON CONFLICT DO NOTHING`, group, msg, len(d.webp), sealed); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `UPDATE group_messages SET attachment = attachment || $3::jsonb WHERE group_id = $1 AND id = $2 AND attachment IS NOT NULL AND deleted_at IS NULL`,
					group, msg, raw)
				return err
			})
		},
	}
}
