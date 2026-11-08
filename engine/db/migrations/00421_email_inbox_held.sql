-- Held email replies back off (docs/email.md#threads, decision 0063).
--
-- A reply whose ticket's run is still going but not waiting stays in
-- the inbox. It used to be claimed again every 5 minutes for up to 7
-- days, each time opened and parsed (up to 25 MB), and, as the oldest
-- rows, it came first in every claim, ahead of fresh mail.
--
-- taskiem_email_inbox_hold records the hold on the row: how many times
-- (held_count) and for which thread (held_thread), and leaves it alone
-- for 5 minutes, then 10, 20, 40 and at most an hour, never past the
-- 7 days a reply is held. The claim takes fresh mail before held rows.
-- When a run starts waiting for a reply (a signal_waits row for
-- email.reply), the trigger below releases the held rows of that thread
-- in that business at once, so the reply reaches it promptly; a held row
-- whose run ended starts a run on its next look, within the hour.
--
-- The functions are owned by taskiem_dispatch; its table-wide grant on
-- email_inbox (00192) covers the new columns (dispatch_columns.txt).
-- +goose Up
ALTER TABLE email_inbox ADD COLUMN held_count int NOT NULL DEFAULT 0, ADD COLUMN held_thread text;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_email_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, endpoint_id uuid, idem bytea, meta bytea, raw bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE email_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM email_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.held_count > 0, x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.endpoint_id, i.idem, i.meta, i.raw, i.created_at
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_email_inbox_hold(p_id uuid, p_thread text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE email_inbox SET held_count = held_count + 1, held_thread = p_thread,
         claimed_until = LEAST(now() + LEAST(interval '5 minutes' * power(2, LEAST(held_count, 4)), interval '1 hour'),
                               GREATEST(created_at + interval '7 days', now() + interval '5 minutes'))
   WHERE id = p_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_email_inbox_hold(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_email_inbox_hold(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_email_inbox_hold(uuid, text) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_email_inbox_release() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE email_inbox SET claimed_until = NULL
   WHERE tenant_id = NEW.tenant_id AND held_thread = NEW.correlation AND claimed_until > now();
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_email_inbox_release() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_email_inbox_release() FROM PUBLIC;
CREATE TRIGGER email_inbox_release AFTER INSERT ON signal_waits
  FOR EACH ROW WHEN (NEW.event = 'email.reply') EXECUTE FUNCTION taskiem_email_inbox_release();

-- +goose Down
DROP TRIGGER email_inbox_release ON signal_waits;
DROP FUNCTION taskiem_email_inbox_release();
DROP FUNCTION taskiem_email_inbox_hold(uuid, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_email_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, endpoint_id uuid, idem bytea, meta bytea, raw bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE email_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM email_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.endpoint_id, i.idem, i.meta, i.raw, i.created_at
$$;
-- +goose StatementEnd
ALTER TABLE email_inbox DROP COLUMN held_thread, DROP COLUMN held_count;
