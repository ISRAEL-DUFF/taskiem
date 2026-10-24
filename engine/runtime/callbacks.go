package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/pii"
)

// callbackKeyPath is where a run's callback key sits in RunStarted: sealed
// as personal data is, so history shows an envelope and the fold opens it.
var callbackKeyPath = pii.Path{Segments: []string{"run", "callback_key"}, Category: "token"}

func newCallbackKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// CallbackRun is what ingest needs to take a call back to a run.
type CallbackRun struct {
	Ref    RunRef
	Env    string
	Status string
	Key    string
}

// Callback finds the run a callback address names and its key. A run that
// never had callback steps has no key: ErrNotFound.
func (s *Store) Callback(ctx context.Context, tenant, run uuid.UUID) (CallbackRun, error) {
	out := CallbackRun{Ref: RunRef{TenantID: tenant, ID: run}}
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT environment, status FROM runs WHERE id = $1`, run).Scan(&out.Env, &out.Status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("run %s: %w", run, ErrNotFound)
			}
			return err
		}
		raw, err := queryHistory(ctx, tx, `SELECT seq, type, COALESCE(step_id, ''), COALESCE(attempt, 0), payload, recorded_at, origin
			FROM run_events WHERE run_id = $1 AND type = $2 ORDER BY seq LIMIT 1`, run, history.RunStarted)
		if err != nil {
			return err
		}
		if len(raw) == 0 {
			return fmt.Errorf("run %s: %w", run, ErrNotFound)
		}
		opened, _, err := s.openHistory(ctx, tx, tenant, raw)
		if err != nil {
			return err
		}
		var p history.RunStartedPayload
		if err := json.Unmarshal(opened[0].Payload, &p); err != nil {
			return err
		}
		out.Key = p.Run.CallbackKey
		return nil
	})
	if err != nil {
		return out, err
	}
	if out.Key == "" {
		return out, fmt.Errorf("run %s has no callbacks: %w", run, ErrNotFound)
	}
	return out, nil
}

// HistoryAfterOpened is HistoryAfter with sealed values opened, for what
// the run sends outside itself: a respond step's answer to the webhook
// caller, as the worker opens a step's input for its provider.
func (s *Store) HistoryAfterOpened(ctx context.Context, ref RunRef, after int64, limit int) ([]history.Event, error) {
	var out []history.Event
	err := db.InTenantTx(ctx, s.Pool, []uuid.UUID{ref.TenantID}, func(tx pgx.Tx) error {
		raw, err := queryHistory(ctx, tx, `SELECT seq, type, COALESCE(step_id, ''), COALESCE(attempt, 0), payload, recorded_at, origin
			FROM run_events WHERE run_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, ref.ID, after, limit)
		if err != nil {
			return err
		}
		out, _, err = s.openHistory(ctx, tx, ref.TenantID, raw)
		return err
	})
	return out, err
}
