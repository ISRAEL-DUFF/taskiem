package assistant

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

// Service is what assistant@1's actions do for a workflow, in its
// business's scope: hand a customer to a person, resume, and read
// WhatsApp's window.
type Service struct {
	Pool  *pgxpool.Pool
	Vault Sealer
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SenderCategory is a channel's senders' personal-data category: a
// WhatsApp sender is a phone number; a Telegram chat id is other.
func SenderCategory(channel string) string {
	if strings.HasPrefix(channel, "whatsapp@") {
		return "phone"
	}
	return "other"
}

func (s *Service) subject(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, channel, sender string) (string, error) {
	return s.Vault.SubjectForTx(ctx, tx, tenant, SenderCategory(channel), sender)
}

// HandOver hands a customer to a person for hours (a workflow's choice,
// when the model is unsure; D13), and tells the owners.
func (s *Service) HandOver(ctx context.Context, tenant uuid.UUID, channel, sender string, hours int) (time.Time, error) {
	if hours <= 0 || hours > MaxHandoverHours {
		hours = Defaults().HandoverHours
	}
	now := s.now().UTC()
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		subject, err := s.subject(ctx, tx, tenant, channel, sender)
		if err != nil {
			return err
		}
		return HandOver(ctx, tx, tenant, channel, subject, sender, SenderCategory(channel), hours, now, "workflow")
	})
	return now.Add(time.Duration(hours) * time.Hour), err
}

// ResumeSender ends a customer's hand-over.
func (s *Service) ResumeSender(ctx context.Context, tenant uuid.UUID, channel, sender string) error {
	return db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		subject, err := s.subject(ctx, tx, tenant, channel, sender)
		if err != nil {
			return err
		}
		return Resume(ctx, tx, tenant, channel, subject)
	})
}

// Window answers whether a customer's 24-hour window is open on a channel,
// and when they last wrote.
// Telegram has no such window: it is always open there.
func (s *Service) Window(ctx context.Context, tenant uuid.UUID, channel, sender string) (bool, *time.Time, error) {
	if !strings.HasPrefix(channel, "whatsapp@") {
		return true, nil, nil
	}
	var open bool
	var last *time.Time
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		subject, err := s.subject(ctx, tx, tenant, channel, sender)
		if err != nil {
			return err
		}
		open, last, err = WindowOf(ctx, tx, tenant, channel, subject, s.now())
		return err
	})
	return open, last, err
}
