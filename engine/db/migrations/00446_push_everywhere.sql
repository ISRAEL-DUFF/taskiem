-- Push everywhere (docs/spec/mobile-app-m4.md N1 to N3): the phone app as
-- a device beside a browser, the kinds of notification each device takes,
-- and a durable queue of pushes filled in the same transaction as what
-- they tell.
--
-- - A device is a browser (webpush: its address and keys) or the phone
--   app (fcm: its Firebase registration token, in endpoint, with no keys).
-- - Each device takes some kinds: business chats, customer chats, orders
--   and money, approvals, alerts.
-- - Every bell item (notifications) for a person who has a device in the
--   business queues a push (push_outbox), by a trigger, so whatever tells
--   the bell also notifies, and a restart loses nothing queued. A business
--   chat's message is queued by the server itself, with the words chat push
--   allows (no document reference), and skipped here.
-- - Approvals waiting are told in the bell too, once per approver and level
--   (whatsapp_notices' channel 'bell').
-- +goose Up
ALTER TABLE push_subscriptions
  ADD COLUMN kind text NOT NULL DEFAULT 'webpush' CHECK (kind IN ('webpush', 'fcm')),
  ADD COLUMN platform text CHECK (platform IS NULL OR platform IN ('android', 'ios')),
  ADD COLUMN kinds text[] NOT NULL DEFAULT ARRAY['business', 'customers', 'orders', 'approvals', 'alerts']
    CHECK (kinds <@ ARRAY['business', 'customers', 'orders', 'approvals', 'alerts']),
  ALTER COLUMN p256dh DROP NOT NULL,
  ALTER COLUMN auth DROP NOT NULL,
  ADD CONSTRAINT push_subscriptions_keys CHECK (kind <> 'webpush' OR (p256dh IS NOT NULL AND auth IS NOT NULL)),
  DROP CONSTRAINT push_subscriptions_endpoint_check,
  ADD CONSTRAINT push_subscriptions_endpoint_check CHECK (char_length(endpoint) <= 4096);

CREATE TABLE push_outbox (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL,
  -- The kind of push: which devices take it.
  kind        text NOT NULL CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts')),
  title       text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  body        text NOT NULL DEFAULT '' CHECK (char_length(body) <= 1000),
  -- A path inside the app it opens.
  link        text NOT NULL CHECK (char_length(link) BETWEEN 1 AND 500 AND link LIKE '/%' AND link NOT LIKE '//%'),
  -- One notification per tag on a device: the next replaces it.
  tag         text NOT NULL CHECK (char_length(tag) BETWEEN 1 AND 200),
  -- A logo, a path inside the app (/v1/links/<business>/logo), or none.
  icon        text CHECK (icon IS NULL OR (char_length(icon) <= 200 AND icon LIKE '/v1/%')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  attempts    int NOT NULL DEFAULT 0,
  next_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX push_outbox_due ON push_outbox (tenant_id, next_at);
ALTER TABLE push_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON push_outbox TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON push_outbox TO taskiem_app;
-- The queue's definers (owned by taskiem_dispatch) see which businesses
-- have pushes due, and the trigger adds to it in any business.
CREATE POLICY dispatch ON push_outbox TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (tenant_id, next_at), INSERT ON push_outbox TO taskiem_dispatch;
CREATE POLICY dispatch ON push_subscriptions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, user_id) ON push_subscriptions TO taskiem_dispatch;

-- The businesses with pushes due: the sender drains each in its own scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM push_outbox o WHERE o.next_at <= now() LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_push_due(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_push_due(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_push_due(int) TO taskiem_app;

-- The kind of push a bell item is, from what it is about; NULL for what is
-- not pushed (a person's own changes, a run that finished, a business
-- chat's message, which the server queues itself).
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_kind(p_kind text, p_group text, p_link text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE
    WHEN p_group = 'changes' OR p_group LIKE 'changes:%' OR p_kind = 'run_finished' OR p_group LIKE 'msg:%' THEN NULL
    WHEN p_group LIKE 'reaction%' THEN 'business'
    WHEN p_group LIKE 'customer:%' THEN 'customers'
    WHEN p_group LIKE 'approval:%' THEN 'approvals'
    WHEN p_group LIKE 'order:%' OR p_group LIKE 'bill:%' OR p_group LIKE 'reminder:%' OR p_group LIKE 'notice:%' OR p_group = 'money'
      OR p_link LIKE '/bills%' OR p_link LIKE '/invoices%' OR p_link LIKE '/trade-orders%' OR p_link LIKE '/storefront/orders%' OR p_link LIKE '/purchases%' THEN 'orders'
    ELSE 'alerts'
  END
$$;
-- +goose StatementEnd

-- A bell item for someone with a device in the business queues a push: on a
-- new item, and again when the same group repeats (its count and title
-- change). It never fails the bell: a push that cannot be queued is lost,
-- not the notice.
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_from_notice() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_kind text;
  v_tag text;
BEGIN
  IF NEW.read_at IS NOT NULL OR (TG_OP = 'UPDATE' AND NEW.count = OLD.count) THEN
    RETURN NEW;
  END IF;
  v_kind := taskiem_push_kind(NEW.kind, NEW.group_key, NEW.link);
  IF v_kind IS NULL OR NOT EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.tenant_id = NEW.tenant_id AND s.user_id = NEW.user_id) THEN
    RETURN NEW;
  END IF;
  -- A conversation's own tag (one notification per chat), else the group's.
  v_tag := COALESCE(substring(NEW.link FROM '^/chat\?with=([A-Za-z0-9-]{1,80})'), NEW.group_key);
  BEGIN
    INSERT INTO push_outbox (id, tenant_id, user_id, kind, title, body, link, tag)
    VALUES (gen_random_uuid(), NEW.tenant_id, NEW.user_id, v_kind, left(NEW.title, 200), left(NEW.body, 1000),
            CASE WHEN NEW.link LIKE '/%' AND NEW.link NOT LIKE '//%' THEN left(NEW.link, 500) ELSE '/' END, left(v_tag, 200));
    PERFORM pg_notify('push_outbox', '');
  EXCEPTION WHEN OTHERS THEN
    RETURN NEW;
  END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_push_from_notice() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_push_from_notice() FROM PUBLIC;
CREATE TRIGGER notifications_push AFTER INSERT OR UPDATE OF count ON notifications
  FOR EACH ROW EXECUTE FUNCTION taskiem_push_from_notice();

-- Approvals waiting, told in the bell once per approver and level.
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web', 'bell'));

-- The businesses with an approval open (requested in the last week), whose
-- approvers the bell tells, business by business in each one's own scope.
GRANT SELECT (tenant_id, status, requested_at) ON approvals TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_approval_tenants() RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT a.tenant_id FROM approvals a WHERE a.status = 'open' AND a.requested_at > now() - interval '7 days'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_approval_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_approval_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_approval_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_approval_tenants();
REVOKE SELECT (tenant_id, status, requested_at) ON approvals FROM taskiem_dispatch;
DELETE FROM whatsapp_notices WHERE channel = 'bell';
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));
DROP TRIGGER notifications_push ON notifications;
DROP FUNCTION taskiem_push_from_notice();
DROP FUNCTION taskiem_push_kind(text, text, text);
DROP FUNCTION taskiem_push_due(int);
REVOKE SELECT (tenant_id, user_id) ON push_subscriptions FROM taskiem_dispatch;
DROP POLICY dispatch ON push_subscriptions;
DROP TABLE push_outbox;
DELETE FROM push_subscriptions WHERE kind <> 'webpush';
ALTER TABLE push_subscriptions
  DROP CONSTRAINT push_subscriptions_endpoint_check,
  ADD CONSTRAINT push_subscriptions_endpoint_check CHECK (char_length(endpoint) <= 2048),
  DROP CONSTRAINT push_subscriptions_keys,
  ALTER COLUMN p256dh SET NOT NULL,
  ALTER COLUMN auth SET NOT NULL,
  DROP COLUMN kinds,
  DROP COLUMN platform,
  DROP COLUMN kind;
