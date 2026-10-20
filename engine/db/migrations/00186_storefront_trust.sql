-- Trust and safety for storefronts and forms (Phase 5, S8; storefronts
-- spec 7, decision 0036, 13; docs/moderation.md):
--
-- - pages wait for review (in_review) and can be suspended;
-- - what the checks found, per text, so nothing is checked twice;
-- - the review queue and reports, which operators work through;
-- - orders paused by the plan or the unverified daily limit;
-- - the Shop plans' limits.
-- +goose Up
ALTER TABLE storefronts DROP CONSTRAINT storefronts_status_check;
ALTER TABLE storefronts ADD CONSTRAINT storefronts_status_check CHECK (status IN ('draft', 'in_review', 'published', 'closed', 'suspended'));
ALTER TABLE storefronts
  -- Why the first publish was turned down, for the owner.
  ADD COLUMN review_note       text,
  ADD COLUMN suspended_reason  text,
  ADD COLUMN suspended_at      timestamptz,
  ADD COLUMN suspended_by      text,
  -- What the page was when suspended (published or closed), to restore.
  ADD COLUMN status_before_suspension text CHECK (status_before_suspension IN ('published', 'closed')),
  -- Set while orders and bookings are paused (the plan's month, or the
  -- unverified daily limit): the forms say so instead of taking them.
  ADD COLUMN orders_paused     text,
  ADD CONSTRAINT storefronts_suspension CHECK ((status = 'suspended') = (suspended_at IS NOT NULL AND status_before_suspension IS NOT NULL));
GRANT SELECT (orders_paused) ON storefronts TO taskiem_dispatch;

-- What the checks said about a piece of text or a price, by its digest:
-- the same text is checked once per organisation.
CREATE TABLE moderation_checks (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  hash        bytea NOT NULL CHECK (length(hash) = 32),
  verdict     text NOT NULL CHECK (verdict IN ('allowed', 'flagged', 'blocked')),
  category    text NOT NULL DEFAULT '',
  reason      text NOT NULL DEFAULT '',
  checker     text NOT NULL,             -- words, price, model
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, hash)
);
ALTER TABLE moderation_checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE moderation_checks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON moderation_checks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON moderation_checks TO taskiem_app;

-- The review queue: a first publish waiting, an edit or catalogue item
-- flagged after going live, a page suspended for reports, an owner's
-- appeal, or a request to verify the business.
CREATE TABLE storefront_reviews (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  kind           text NOT NULL CHECK (kind IN ('first_publish', 'edit', 'catalogue', 'reports', 'appeal', 'business')),
  version        int,
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
  -- What the checks found ({field, text, verdict, category, reason, checker}),
  -- or what the owner sent (an appeal's message, a business's details).
  findings       jsonb NOT NULL DEFAULT '[]',
  detail         jsonb NOT NULL DEFAULT '{}',
  decided_by     text,
  decision_note  text,
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  decided_at     timestamptz,
  CHECK ((status = 'pending') = (decided_at IS NULL))
);
CREATE INDEX storefront_reviews_pending ON storefront_reviews (created_at) WHERE status = 'pending';
CREATE INDEX storefront_reviews_tenant ON storefront_reviews (tenant_id, created_at DESC);
-- One pending review of each kind per page (a new edit joins it).
CREATE UNIQUE INDEX storefront_reviews_one ON storefront_reviews (storefront_id, kind) WHERE status = 'pending';
ALTER TABLE storefront_reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_reviews FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_reviews TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON storefront_reviews TO taskiem_app;
CREATE POLICY dispatch ON storefront_reviews FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON storefront_reviews TO taskiem_dispatch;

-- Reports from a page's or a form's footer. The reporter's contact, if
-- they give one, is sealed by the public host for the api role; the
-- address is kept only as a keyed digest, to count distinct reporters.
CREATE TABLE page_reports (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  kind           text NOT NULL CHECK (kind IN ('storefront', 'form')),
  site_id        uuid NOT NULL,       -- the storefront, or the form endpoint
  reason         text NOT NULL CHECK (reason IN ('scam', 'impersonation', 'prohibited', 'offensive', 'other')),
  message        text NOT NULL DEFAULT '' CHECK (length(message) <= 500),
  contact        bytea,
  ip_hash        bytea NOT NULL CHECK (length(ip_hash) = 32),
  status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'dismissed', 'actioned')),
  handled_by     text,
  handled_at     timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX page_reports_open ON page_reports (site_id, created_at) WHERE status = 'open';
CREATE INDEX page_reports_tenant ON page_reports (tenant_id, created_at DESC);
ALTER TABLE page_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON page_reports TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, UPDATE (status, handled_by, handled_at) ON page_reports TO taskiem_app;
CREATE POLICY dispatch ON page_reports TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON page_reports TO taskiem_dispatch;
GRANT SELECT (id, tenant_id, status) ON form_endpoints TO taskiem_dispatch;

-- The Shop plans: their tier, and their limits (0: no limit).
ALTER TABLE plans DROP CONSTRAINT plans_tier_check;
ALTER TABLE plans ADD CONSTRAINT plans_tier_check CHECK (tier IN ('internal', 'shop', 'starter', 'growth', 'business', 'enterprise'));
ALTER TABLE tenant_limits
  ADD COLUMN storefront_items            int CHECK (storefront_items >= 0),
  ADD COLUMN storefront_orders_monthly   bigint CHECK (storefront_orders_monthly >= 0),
  ADD COLUMN storefront_bookings_monthly bigint CHECK (storefront_bookings_monthly >= 0),
  ADD COLUMN sms_monthly                 bigint CHECK (sms_monthly >= 0),
  ADD COLUMN embed_sites                 int CHECK (embed_sites >= 0);
CREATE INDEX platform_messages_month ON platform_messages (tenant_id, channel, created_at);

-- The page as the public host serves it, now with whether the business
-- is verified (the badge) and whether orders are paused. A suspended page
-- answers too, so the host can say it is unavailable.
DROP FUNCTION taskiem_public_storefront(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront(p_storefront uuid)
RETURNS TABLE (status text, version int, model jsonb, closed_message text, verified boolean, orders_paused text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.published_version, n.model, s.closed_message,
         EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = s.tenant_id AND v.level IN ('business', 'agent_onboarded')),
         s.orders_paused
    FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN storefront_snapshots n ON n.storefront_id = s.id AND n.version = s.published_version
   WHERE s.id = p_storefront AND s.status IN ('published', 'closed', 'suspended')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_storefront(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_storefront(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_storefront(uuid) TO taskiem_public;

-- +goose StatementBegin
-- A report from a page's footer: at most three a day from one address
-- for one page. False when it is not taken.
CREATE FUNCTION taskiem_public_report(p_kind text, p_site text, p_reason text, p_message text, p_contact bytea, p_ip_hash bytea)
RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  sid uuid;
BEGIN
  IF p_kind = 'storefront' THEN
    sid := p_site::uuid;
    SELECT s.tenant_id INTO t FROM storefronts s WHERE s.id = sid AND s.status IN ('published', 'closed', 'suspended');
  ELSIF p_kind = 'form' THEN
    SELECT e.id, e.tenant_id INTO sid, t FROM form_endpoints e WHERE e.public_id = p_site;
  END IF;
  IF t IS NULL OR p_reason NOT IN ('scam', 'impersonation', 'prohibited', 'offensive', 'other') THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM page_reports r WHERE r.site_id = sid AND r.ip_hash = p_ip_hash AND r.created_at > now() - interval '1 day') >= 3 THEN
    RETURN false;
  END IF;
  INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, message, contact, ip_hash)
    VALUES (gen_random_uuid(), t, p_kind, sid, p_reason, left(COALESCE(p_message, ''), 500), p_contact, p_ip_hash);
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_report(text, text, text, text, bytea, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_report(text, text, text, text, bytea, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_report(text, text, text, text, bytea, bytea) TO taskiem_public;
GRANT SELECT (public_id) ON form_endpoints TO taskiem_dispatch;

-- +goose StatementBegin
-- Whether a business proved a phone: a phone verification, or an owner
-- or admin with a linked WhatsApp number or Telegram account.
CREATE FUNCTION taskiem_tenant_phone_verified(p_tenant uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = p_tenant AND v.level IN ('phone', 'business', 'agent_onboarded'))
      OR EXISTS (SELECT 1 FROM memberships m JOIN whatsapp_bindings b ON b.user_id = m.user_id
                  WHERE m.tenant_id = p_tenant AND m.role IN ('owner', 'admin'))
      OR EXISTS (SELECT 1 FROM memberships m JOIN telegram_bindings l ON l.user_id = m.user_id
                  WHERE m.tenant_id = p_tenant AND m.role IN ('owner', 'admin'))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: the review queue, oldest first, with what a reviewer needs
-- to decide quickly.
CREATE FUNCTION taskiem_ops_storefront_reviews(p_status text)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, storefront_id uuid, slug text, page_status text, kind text, version int,
  status text, findings jsonb, detail jsonb, created_by text, created_at timestamptz, decided_by text, decision_note text, decided_at timestamptz,
  verified boolean, agent_code text, open_reports bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.id, r.tenant_id, t.name, r.storefront_id, s.slug, s.status, r.kind, r.version, r.status, r.findings, r.detail, r.created_by, r.created_at,
         r.decided_by, r.decision_note, r.decided_at,
         EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = r.tenant_id AND v.level IN ('business', 'agent_onboarded')),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id),
         (SELECT count(*) FROM page_reports p WHERE p.site_id = r.storefront_id AND p.status = 'open')
    FROM storefront_reviews r JOIN tenants t ON t.id = r.tenant_id JOIN storefronts s ON s.id = r.storefront_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY CASE WHEN r.status = 'pending' THEN r.created_at END ASC, r.created_at DESC
   LIMIT 200
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: reports, newest first, with the page they are about.
CREATE FUNCTION taskiem_ops_page_reports(p_status text)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, kind text, site_id uuid, slug text, reason text, message text, has_contact boolean,
  status text, created_at timestamptz, handled_by text, handled_at timestamptz, reports_on_site bigint, agent_code text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, p.tenant_id, t.name, p.kind, p.site_id, (SELECT s.slug FROM storefronts s WHERE s.id = p.site_id), p.reason, p.message,
         p.contact IS NOT NULL, p.status, p.created_at, p.handled_by, p.handled_at,
         (SELECT count(DISTINCT q.ip_hash) FROM page_reports q WHERE q.site_id = p.site_id AND q.status = 'open'),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id)
    FROM page_reports p JOIN tenants t ON t.id = p.tenant_id
   WHERE p.status = p_status OR p_status = 'all'
   ORDER BY p.created_at DESC LIMIT 200
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Pages reported by enough different people to suspend pending review:
-- published or closed pages with open reports from at least p_min
-- distinct addresses in the last 30 days.
CREATE FUNCTION taskiem_reported_pages(p_min int)
RETURNS TABLE (tenant_id uuid, storefront_id uuid, reporters bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id, s.id, count(DISTINCT p.ip_hash)
    FROM page_reports p JOIN storefronts s ON s.id = p.site_id
   WHERE p.kind = 'storefront' AND p.status = 'open' AND p.created_at > now() - interval '30 days' AND s.status IN ('published', 'closed')
   GROUP BY s.tenant_id, s.id HAVING count(DISTINCT p.ip_hash) >= p_min
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Live pages, for the scheduler's check of orders against the plan and
-- the unverified daily limit.
CREATE FUNCTION taskiem_live_storefronts()
RETURNS TABLE (tenant_id uuid, storefront_id uuid, orders_paused text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id, s.id, s.orders_paused FROM storefronts s JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.status IN ('published', 'closed')
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: the agents, now with the open reports against the businesses
-- they signed up (storefronts 6.10, accountability).
DROP FUNCTION taskiem_ops_agents();
CREATE FUNCTION taskiem_ops_agents()
RETURNS TABLE (id uuid, code text, name text, phone text, region text, status text, checks jsonb, registered_by text, created_at timestamptz,
  businesses bigint, published bigint, verified bigint, live_sessions bigint, reports bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.code, a.name, a.phone, a.region, a.status, a.checks, a.registered_by, a.created_at,
         (SELECT count(*) FROM tenants t WHERE t.agent_id = a.id),
         (SELECT count(*) FROM tenants t JOIN storefronts f ON f.tenant_id = t.id AND f.published_version > 0 WHERE t.agent_id = a.id),
         (SELECT count(DISTINCT v.tenant_id) FROM tenant_verifications v JOIN tenants t ON t.id = v.tenant_id
           WHERE t.agent_id = a.id AND v.level = 'agent_onboarded'),
         (SELECT count(*) FROM agent_sessions s WHERE s.agent_id = a.id AND s.status = 'active' AND s.expires_at > now()),
         (SELECT count(*) FROM page_reports p JOIN tenants t ON t.id = p.tenant_id WHERE t.agent_id = a.id AND p.status <> 'dismissed')
    FROM agents a ORDER BY a.created_at DESC
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_tenant_phone_verified(uuid)', 'taskiem_ops_storefront_reviews(text)', 'taskiem_ops_page_reports(text)',
    'taskiem_reported_pages(int)', 'taskiem_live_storefronts()', 'taskiem_ops_agents()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT (id, name, agent_id, status) ON tenants TO taskiem_dispatch;
GRANT SELECT (id, slug, status, tenant_id, published_version) ON storefronts TO taskiem_dispatch;

-- An owner agreeing to a field agent from Telegram (decision 0036, 16):
-- the code they sent waits here while Telegram asks them to share their
-- own contact, which proves the number. Ten minutes, then gone.
CREATE TABLE telegram_agent_codes (
  telegram_id  bigint PRIMARY KEY CHECK (telegram_id > 0),
  code         text NOT NULL CHECK (code ~ '^[0-9]{6}$'),
  expires_at   timestamptz NOT NULL
);
ALTER TABLE telegram_agent_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE telegram_agent_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON telegram_agent_codes TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON telegram_agent_codes TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON telegram_agent_codes FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON telegram_agent_codes TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_agree_put(p_telegram_id bigint, p_code text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO telegram_agent_codes (telegram_id, code, expires_at) VALUES (p_telegram_id, p_code, now() + interval '10 minutes')
  ON CONFLICT (telegram_id) DO UPDATE SET code = EXCLUDED.code, expires_at = EXCLUDED.expires_at
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The code waiting for an account, once: it is gone after.
CREATE FUNCTION taskiem_tg_agree_take(p_telegram_id bigint)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH x AS (DELETE FROM telegram_agent_codes c WHERE c.telegram_id = p_telegram_id RETURNING c.code, c.expires_at)
  SELECT code FROM x WHERE expires_at > now()
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_agent_session_consent(text, text);
-- +goose StatementBegin
-- The owner sent a consent code from their number. The code must match a
-- pending onboarding for that number; five wrong codes lock them all.
-- On a match the onboarding is active for 24 hours, the business records
-- its agent, and the owner's channel is bound to them if it is bound to
-- no one: the WhatsApp number it came from, or (p_telegram_id) the
-- Telegram account that shared it as its own contact (S8).
-- Outcomes: ok, wrong, locked, none, number_taken.
CREATE FUNCTION taskiem_agent_session_consent(p_number text, p_code text, p_telegram_id bigint)
RETURNS TABLE (outcome text, session_id uuid, tenant_id uuid, tenant_name text, agent_name text, agent_code text, owner_user uuid, expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  s agent_sessions;
  h bytea := sha256(convert_to(p_number || ':' || p_code, 'UTF8'));
  e timestamptz := now() + interval '24 hours';
  a agents;
  tname text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now()) THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  SELECT * INTO s FROM agent_sessions x
   WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5 AND x.consent_hash = h
   FOR UPDATE;
  IF NOT FOUND THEN
    UPDATE agent_sessions x SET consent_failures = x.consent_failures + 1
     WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now();
    IF EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5) THEN
      RETURN QUERY SELECT 'wrong'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
    END IF;
    RETURN QUERY SELECT 'locked'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  IF (p_telegram_id IS NULL AND EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> s.owner_user))
     OR (p_telegram_id IS NOT NULL AND EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.telegram_id = p_telegram_id AND b.user_id <> s.owner_user)) THEN
    RETURN QUERY SELECT 'number_taken'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  UPDATE agent_sessions x SET status = 'active', consent_at = now(), expires_at = e, consent_hash = NULL WHERE x.id = s.id;
  UPDATE tenants t SET agent_id = s.agent_id WHERE t.id = s.tenant_id AND t.agent_id IS NULL;
  IF p_telegram_id IS NULL AND NOT EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = s.owner_user) THEN
    INSERT INTO whatsapp_bindings (user_id, number) VALUES (s.owner_user, p_number);
  ELSIF p_telegram_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.user_id = s.owner_user) THEN
    INSERT INTO telegram_bindings (user_id, telegram_id) VALUES (s.owner_user, p_telegram_id);
  END IF;
  SELECT * INTO a FROM agents x WHERE x.id = s.agent_id;
  SELECT t.name INTO tname FROM tenants t WHERE t.id = s.tenant_id;
  RETURN QUERY SELECT 'ok'::text, s.id, s.tenant_id, tname, a.name, a.code, s.owner_user, e;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_tg_agree_put(bigint, text)', 'taskiem_tg_agree_take(bigint)', 'taskiem_agent_session_consent(text, text, bigint)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_agent_session_consent(text, text, bigint);
-- +goose StatementBegin
-- The owner sent a consent code from their number. The code must match a
-- pending onboarding for that number; five wrong codes lock them all.
-- On a match the onboarding is active for 24 hours, the business records
-- its agent, and the number is bound to the owner if it is bound to no one.
-- Outcomes: ok, wrong, locked, none, number_taken.
CREATE FUNCTION taskiem_agent_session_consent(p_number text, p_code text)
RETURNS TABLE (outcome text, session_id uuid, tenant_id uuid, tenant_name text, agent_name text, agent_code text, owner_user uuid, expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  s agent_sessions;
  h bytea := sha256(convert_to(p_number || ':' || p_code, 'UTF8'));
  e timestamptz := now() + interval '24 hours';
  a agents;
  tname text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now()) THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  SELECT * INTO s FROM agent_sessions x
   WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5 AND x.consent_hash = h
   FOR UPDATE;
  IF NOT FOUND THEN
    UPDATE agent_sessions x SET consent_failures = x.consent_failures + 1
     WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now();
    IF EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5) THEN
      RETURN QUERY SELECT 'wrong'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
    END IF;
    RETURN QUERY SELECT 'locked'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> s.owner_user) THEN
    RETURN QUERY SELECT 'number_taken'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  UPDATE agent_sessions x SET status = 'active', consent_at = now(), expires_at = e, consent_hash = NULL WHERE x.id = s.id;
  UPDATE tenants t SET agent_id = s.agent_id WHERE t.id = s.tenant_id AND t.agent_id IS NULL;
  IF NOT EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = s.owner_user) THEN
    INSERT INTO whatsapp_bindings (user_id, number) VALUES (s.owner_user, p_number);
  END IF;
  SELECT * INTO a FROM agents x WHERE x.id = s.agent_id;
  SELECT t.name INTO tname FROM tenants t WHERE t.id = s.tenant_id;
  RETURN QUERY SELECT 'ok'::text, s.id, s.tenant_id, tname, a.name, a.code, s.owner_user, e;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_agent_session_consent(text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_agent_session_consent(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_agent_session_consent(text, text) TO taskiem_app;
DROP FUNCTION taskiem_tg_agree_take(bigint);
DROP FUNCTION taskiem_tg_agree_put(bigint, text);
DROP TABLE telegram_agent_codes;
DROP FUNCTION taskiem_ops_agents();
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_agents()
RETURNS TABLE (id uuid, code text, name text, phone text, region text, status text, checks jsonb, registered_by text, created_at timestamptz,
  businesses bigint, published bigint, verified bigint, live_sessions bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.code, a.name, a.phone, a.region, a.status, a.checks, a.registered_by, a.created_at,
         (SELECT count(*) FROM tenants t WHERE t.agent_id = a.id),
         (SELECT count(*) FROM tenants t JOIN storefronts f ON f.tenant_id = t.id AND f.published_version > 0 WHERE t.agent_id = a.id),
         (SELECT count(DISTINCT v.tenant_id) FROM tenant_verifications v JOIN tenants t ON t.id = v.tenant_id
           WHERE t.agent_id = a.id AND v.level = 'agent_onboarded'),
         (SELECT count(*) FROM agent_sessions s WHERE s.agent_id = a.id AND s.status = 'active' AND s.expires_at > now())
    FROM agents a ORDER BY a.created_at DESC
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_agents() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_agents() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_agents() TO taskiem_app;
DROP FUNCTION taskiem_live_storefronts();
DROP FUNCTION taskiem_reported_pages(int);
DROP FUNCTION taskiem_ops_page_reports(text);
DROP FUNCTION taskiem_ops_storefront_reviews(text);
DROP FUNCTION taskiem_tenant_phone_verified(uuid);
DROP FUNCTION taskiem_public_report(text, text, text, text, bytea, bytea);
DROP FUNCTION taskiem_public_storefront(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront(p_storefront uuid)
RETURNS TABLE (status text, version int, model jsonb, closed_message text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.published_version, n.model, s.closed_message
    FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN storefront_snapshots n ON n.storefront_id = s.id AND n.version = s.published_version
   WHERE s.id = p_storefront AND s.status IN ('published', 'closed')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_storefront(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_storefront(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_storefront(uuid) TO taskiem_public;
DROP INDEX platform_messages_month;
UPDATE plans SET active = false, tier = 'starter' WHERE tier = 'shop';
ALTER TABLE plans DROP CONSTRAINT plans_tier_check;
ALTER TABLE plans ADD CONSTRAINT plans_tier_check CHECK (tier IN ('internal', 'starter', 'growth', 'business', 'enterprise'));
ALTER TABLE tenant_limits DROP COLUMN storefront_items, DROP COLUMN storefront_orders_monthly, DROP COLUMN storefront_bookings_monthly,
  DROP COLUMN sms_monthly, DROP COLUMN embed_sites;
DROP TABLE page_reports;
DROP TABLE storefront_reviews;
DROP TABLE moderation_checks;
UPDATE storefronts SET status = COALESCE(status_before_suspension, 'published') WHERE status = 'suspended';
UPDATE storefronts SET status = 'draft' WHERE status = 'in_review';
ALTER TABLE storefronts DROP CONSTRAINT storefronts_suspension;
ALTER TABLE storefronts DROP COLUMN review_note, DROP COLUMN suspended_reason, DROP COLUMN suspended_at, DROP COLUMN suspended_by,
  DROP COLUMN status_before_suspension, DROP COLUMN orders_paused;
ALTER TABLE storefronts DROP CONSTRAINT storefronts_status_check;
ALTER TABLE storefronts ADD CONSTRAINT storefronts_status_check CHECK (status IN ('draft', 'published', 'closed'));
