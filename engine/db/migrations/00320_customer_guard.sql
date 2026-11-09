-- Customer guard (decision 0062 V6; docs/assistant-guard.md): what keeps a
-- business's assistant safe for its customers and the business.
--
-- runs.customer_subject     whose message started a run (the sender's
--                           subject, a keyed hash, never the number): its
--                           free text and names are sealed under it, so
--                           erasing the customer erases them (P2).
-- assistant_contacts        one row per customer per channel (a connector
--                           and connection): when they first and last wrote
--                           (WhatsApp's 24-hour window, C13), their counts
--                           this minute and today (L1), STOP (P3) and a
--                           hand-over to a person (K5). Keyed by subject;
--                           the sender itself is kept sealed, for members.
-- tenant_channel_settings   the owner's pause for every customer (L11).
-- assistant_controls        Taskiem's own switches for one business: the
--                           beta flag (E4) and a stop (L11), written only
--                           from the operators' console (the platform
--                           chain in scope).

-- +goose Up
-- A chat trigger's customer guard (wd/v1 connector_event config.guard).
ALTER TABLE triggers ADD COLUMN guard jsonb;

ALTER TABLE runs ADD COLUMN customer_subject text;
CREATE INDEX runs_customer ON runs (tenant_id, customer_subject) WHERE customer_subject IS NOT NULL;

CREATE TABLE assistant_contacts (
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  channel          text NOT NULL,                 -- <connector ref>/<connection>
  subject          text NOT NULL,                 -- the sender's subject
  sender           jsonb NOT NULL,                -- the sender, sealed under its subject
  language         text NOT NULL DEFAULT 'en',    -- the language of their last message we were sure of
  first_at         timestamptz NOT NULL DEFAULT now(),
  last_inbound_at  timestamptz NOT NULL DEFAULT now(),
  minute_at        timestamptz NOT NULL DEFAULT now(),
  minute_count     int NOT NULL DEFAULT 0,
  day              date NOT NULL DEFAULT current_date,
  day_count        int NOT NULL DEFAULT 0,
  limited_at       timestamptz,                   -- when they were last told they wrote too much
  stopped_at       timestamptz,                   -- they wrote STOP
  handover_at      timestamptz,                   -- they asked for a person (or a workflow handed them over)
  handover_until   timestamptz,
  PRIMARY KEY (tenant_id, channel, subject)
);
CREATE INDEX assistant_contacts_handover ON assistant_contacts (tenant_id, handover_at) WHERE handover_at IS NOT NULL;
CREATE INDEX assistant_contacts_subject ON assistant_contacts (tenant_id, subject);
ALTER TABLE assistant_contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assistant_contacts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON assistant_contacts TO taskiem_app;

ALTER TABLE tenant_channel_settings
  ADD COLUMN assistant_paused_at timestamptz,
  ADD COLUMN assistant_paused_by text;

CREATE TABLE assistant_controls (
  tenant_id      uuid PRIMARY KEY REFERENCES tenants(id),
  beta           boolean NOT NULL DEFAULT false,
  stopped_at     timestamptz,
  stopped_reason text,
  updated_by     text NOT NULL,
  updated_at     timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE assistant_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON assistant_controls FOR SELECT TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope()));
-- Only the operators' console writes, with the platform chain in scope.
CREATE POLICY operators_write ON assistant_controls FOR INSERT TO taskiem_app
  WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()) AND 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope()));
CREATE POLICY operators_update ON assistant_controls FOR UPDATE TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope()) AND 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()) AND 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON assistant_controls TO taskiem_app;

-- +goose Down
DROP TABLE assistant_controls;
ALTER TABLE tenant_channel_settings DROP COLUMN assistant_paused_at, DROP COLUMN assistant_paused_by;
DROP TABLE assistant_contacts;
DROP INDEX runs_customer;
ALTER TABLE runs DROP COLUMN customer_subject;
ALTER TABLE triggers DROP COLUMN guard;
