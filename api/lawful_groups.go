package api

import (
	"context"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/peoplechats"
)

// A lawful request's group messages (people-chatting §8.5; 5b B; decision
// 0069 point 11): when the request asks for them (groups: sent or seen),
// the export holds, for each group the person is or was in, the messages
// of the dates asked as the person's own copy holds them (row-level
// security keeps them to the person's own time in the group; never one
// they deleted for themselves): their own (sent), or every one they could
// see (seen); with one other person named (with_person_id), only theirs
// and the person's. Photos and files only when the request asks for files.

type lawfulGroupMessage struct {
	ID         uuid.UUID       `json:"id"`
	SenderID   *uuid.UUID      `json:"sender_id"`
	Sender     string          `json:"sender"`
	FromPerson bool            `json:"from_person"`
	Kind       string          `json:"kind"`
	Body       string          `json:"body"`
	Card       *peopleCard     `json:"card,omitempty"`
	Location   *peopleLocation `json:"location,omitempty"`
	Attachment map[string]any  `json:"attachment,omitempty"`
	File       *string         `json:"file,omitempty"`
	Deleted    bool            `json:"deleted,omitempty"`
	At         time.Time       `json:"at"`
}

type lawfulGroup struct {
	GroupID  uuid.UUID            `json:"group_id"`
	Name     string               `json:"name"`
	Messages []lawfulGroupMessage `json:"messages"`
}

// lawfulGroupsExport reads the person's group messages within the
// request's scope, in the person's own transaction; answers the groups
// with messages, how many messages and how many files.
func (s *Server) lawfulGroupsExport(ctx context.Context, person uuid.UUID, lr lawfulRequest) ([]lawfulGroup, int, int, error) {
	var out []lawfulGroup
	given, files := 0, 0
	err := db.InPersonTx(ctx, s.Store.Pool, person, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT g.id, g.sealed, m.left_sealed, m.left_at IS NULL FROM group_members m JOIN people_groups g ON g.id = m.group_id
			WHERE m.person_id = $1 ORDER BY g.id`, person)
		if err != nil {
			return err
		}
		type ref struct {
			id           uuid.UUID
			sealed, left []byte
			current      bool
		}
		refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ref, error) {
			var x ref
			return x, r.Scan(&x.id, &x.sealed, &x.left, &x.current)
		})
		if err != nil {
			return err
		}
		for _, g := range refs {
			key, err := s.keys().GroupKeyTx(ctx, tx, g.id)
			if errors.Is(err, peoplechats.ErrNoKey) {
				continue
			}
			if err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT x.id, x.sender_id, x.kind, x.sealed, x.attachment, x.deleted_at IS NOT NULL, x.created_at, f.sealed
				FROM group_messages x LEFT JOIN group_files f ON $4 AND f.group_id = x.group_id AND f.message_id = x.id
				LEFT JOIN group_marks k ON k.group_id = x.group_id AND k.message_id = x.id AND k.person_id = $1
				WHERE x.group_id = $2 AND x.kind <> 'event' AND k.hidden_at IS NULL AND x.created_at >= $3 AND x.created_at < $5
				  AND CASE WHEN $6 = 'sent' THEN x.sender_id = $1 ELSE ($7::uuid IS NULL OR x.sender_id = $1 OR x.sender_id = $7) END
				ORDER BY x.created_at, x.id`, person, g.id, lr.From, lr.Files, lr.To, lr.Groups, lr.WithPersonID)
			if err != nil {
				return err
			}
			var senders []uuid.UUID
			msgs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (lawfulGroupMessage, error) {
				var m lawfulGroupMessage
				var sealed, file []byte
				if err := r.Scan(&m.ID, &m.SenderID, &m.Kind, &sealed, &m.Attachment, &m.Deleted, &m.At, &file); err != nil {
					return m, err
				}
				if m.SenderID != nil {
					m.FromPerson = *m.SenderID == person
					senders = append(senders, *m.SenderID)
				}
				if v, err := openGroupSealed(key, g.id, m.ID, sealed); err == nil && !m.Deleted {
					m.Body, m.Card, m.Location = v.Body, v.Card, v.Location
					if m.Attachment != nil && v.Name != "" {
						m.Attachment["name"] = v.Name
					}
				}
				if file != nil && !m.Deleted {
					if data, err := peoplechats.Open(key, g.id, groupFileRow(m.ID), file); err == nil {
						b := base64.StdEncoding.EncodeToString(data)
						m.File = &b
						files++
					}
				}
				return m, nil
			})
			if err != nil {
				return err
			}
			if len(msgs) == 0 {
				continue
			}
			_, cards, err := s.groupNamesTx(ctx, tx, person, senders)
			if err != nil {
				return err
			}
			for i := range msgs {
				m := &msgs[i]
				switch {
				case m.SenderID == nil:
					m.Sender = peopleGoneName
				case m.FromPerson:
					m.Sender = "The person"
				default:
					if c, ok := cards[*m.SenderID]; ok && c.Name != "" && !(c.Deleted && !c.Suspended) {
						m.Sender = c.Name
					} else {
						m.Sender = peopleGoneName
					}
				}
			}
			info := g.sealed
			if !g.current && g.left != nil {
				info = g.left
			}
			given += len(msgs)
			out = append(out, lawfulGroup{GroupID: g.id, Name: openGroupInfo(key, g.id, info).Name, Messages: msgs})
		}
		return nil
	})
	return out, given, files, err
}
