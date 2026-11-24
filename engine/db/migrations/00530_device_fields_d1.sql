-- Device fields D1 (docs/spec/device-fields.md §14, as built D1).
--
-- form_request_doors: a form step to the person working a business link
-- (to: {link}) stays in the asking business; the side asked keeps this
-- note of it in its own scope, written through the door, so it is listed
-- under To fill in there and answered through the door. Its id is the
-- request's.
--
-- form_chat_answers: what a person has sent in WhatsApp or Telegram for
-- a form step so far (a location, a photo's reference), in the asking
-- business, until the question is whole. A location is sealed.
--
-- deliveries.proof_where and proof_mark, delivery_proofs.source: the
-- proof a form step gave a delivery (riders@1 record_proof): the masked
-- point and how it was got, and whether a photo was taken by the phone's
-- camera or sent, for the Confirm card.
-- +goose Up
CREATE TABLE form_request_doors (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  from_tenant  uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL,
  business     text NOT NULL CHECK (length(business) <= 200),
  title        text NOT NULL CHECK (length(title) <= 200),
  status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered')),
  expires_at   timestamptz NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  answered_at  timestamptz
);
CREATE INDEX form_request_doors_user ON form_request_doors (tenant_id, user_id, created_at DESC) WHERE status = 'pending';
ALTER TABLE form_request_doors ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_request_doors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_request_doors TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON form_request_doors TO taskiem_app;

CREATE TABLE form_chat_answers (
  request_id  uuid PRIMARY KEY REFERENCES form_requests(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL,
  answers     jsonb NOT NULL DEFAULT '{}' CHECK (length(answers::text) <= 16384),
  -- Whole, but weaker than a field's require_source: waiting for the
  -- person to answer in the app or the browser, or to send it as it is.
  held        boolean NOT NULL DEFAULT false,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE form_chat_answers ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_chat_answers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_chat_answers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON form_chat_answers TO taskiem_app;

ALTER TABLE deliveries ADD COLUMN proof_where jsonb CHECK (proof_where IS NULL OR length(proof_where::text) <= 1000),
  ADD COLUMN proof_mark text CHECK (proof_mark IS NULL OR proof_mark IN ('confirmed_at_door', 'far_from_address', 'said_by_rider', 'read_by_phone', 'no_location'));
ALTER TABLE delivery_proofs ADD COLUMN source text CHECK (source IS NULL OR source IN ('camera', 'sent'));

-- A form step answered in WhatsApp or Telegram.
ALTER TABLE form_submissions DROP CONSTRAINT form_submissions_channel_check;
ALTER TABLE form_submissions ADD CONSTRAINT form_submissions_channel_check CHECK (channel IN ('direct', 'embed', 'app', 'whatsapp', 'telegram'));

-- +goose Down
UPDATE form_submissions SET channel = 'app' WHERE channel IN ('whatsapp', 'telegram');
ALTER TABLE form_submissions DROP CONSTRAINT form_submissions_channel_check;
ALTER TABLE form_submissions ADD CONSTRAINT form_submissions_channel_check CHECK (channel IN ('direct', 'embed', 'app'));
ALTER TABLE delivery_proofs DROP COLUMN source;
ALTER TABLE deliveries DROP COLUMN proof_mark, DROP COLUMN proof_where;
DROP TABLE form_chat_answers;
DROP TABLE form_request_doors;
