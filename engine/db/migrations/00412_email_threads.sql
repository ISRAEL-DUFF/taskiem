-- Email threads (decision 0063, docs/spec/tickets.md): a workflow's
-- address takes a reply tag per ticket, and the emails Taskiem sends in a
-- thread (email@1 send_in_thread) are recorded so a reply that names one
-- of them (In-Reply-To, References) finds its ticket.
-- - email_endpoints.reply_key: the address's secret, 32 random bytes made
--   by the api the first time it sends in a thread. Reply tags and the
--   Message-IDs sent are derived from it, so neither can be guessed.
-- - email_thread_messages: one row per email sent in a thread, with no
--   content. It goes with the run that sent it (purged at the end of the
--   run's retention: ON DELETE CASCADE) and with its address. The
--   recipient is kept only as their personal-data pseudonym (the subject
--   id their email is sealed under), for the [#T-1042] subject match from
--   someone already in the thread; erasing them clears it (the trigger
--   below runs in the erasing transaction, as the tenant).
-- - email_messages.status 'replied': a message delivered to the run
--   waiting for it rather than starting one.
-- +goose Up
ALTER TABLE email_endpoints ADD COLUMN reply_key bytea CHECK (reply_key IS NULL OR length(reply_key) = 32);

ALTER TABLE email_messages DROP CONSTRAINT email_messages_status_check;
ALTER TABLE email_messages ADD CONSTRAINT email_messages_status_check CHECK (status IN ('started', 'refused', 'confirmation', 'replied'));

CREATE TABLE email_thread_messages (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  endpoint_id        uuid NOT NULL REFERENCES email_endpoints(id) ON DELETE CASCADE,
  thread             text NOT NULL CHECK (length(thread) <= 24 AND thread ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'),
  message_id         text NOT NULL CHECK (length(message_id) BETWEEN 3 AND 250),
  run_id             uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  recipient_subject  text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (endpoint_id, message_id)
);
CREATE INDEX email_thread_messages_thread ON email_thread_messages (endpoint_id, thread, created_at DESC);
CREATE INDEX email_thread_messages_run ON email_thread_messages (run_id);
CREATE INDEX email_thread_messages_subject ON email_thread_messages (tenant_id, recipient_subject) WHERE recipient_subject IS NOT NULL;
ALTER TABLE email_thread_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_thread_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_thread_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON email_thread_messages TO taskiem_app;
GRANT UPDATE (recipient_subject) ON email_thread_messages TO taskiem_app;

-- A recipient erased is no longer found by their pseudonym. Not a
-- definer: erasure (secrets.Vault.Erase) updates subject_keys as
-- taskiem_app in the tenant's scope, which may update these rows.
-- +goose StatementBegin
CREATE FUNCTION taskiem_email_thread_recipients_erased() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE email_thread_messages SET recipient_subject = NULL
   WHERE tenant_id = NEW.tenant_id AND recipient_subject = NEW.subject_id;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER email_thread_recipients_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_email_thread_recipients_erased();

-- +goose Down
DROP TRIGGER email_thread_recipients_erased ON subject_keys;
DROP FUNCTION taskiem_email_thread_recipients_erased();
DROP TABLE email_thread_messages;
DELETE FROM email_messages WHERE status = 'replied';
ALTER TABLE email_messages DROP CONSTRAINT email_messages_status_check;
ALTER TABLE email_messages ADD CONSTRAINT email_messages_status_check CHECK (status IN ('started', 'refused', 'confirmation'));
ALTER TABLE email_endpoints DROP COLUMN reply_key;
