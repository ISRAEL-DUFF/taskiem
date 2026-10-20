-- +goose Up
-- Hosted forms (docs/spec/forms.md, Phase 5 S7): a form trigger's page on
-- the form host, the form step's one-time links, verification codes, and
-- the inbox the public role writes submissions to. As for storefronts,
-- the public role holds no vault key: it seals what it receives with the
-- public key (TASKIEM_PUBLIC_KEY), and the api role opens it, seals the
-- personal answers under the respondent's subject, keeps files in the
-- organisation's Xenibase storage, and starts the run or answers the step.

ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form'));

-- One live form per workflow and environment, at /f/{public_id}.
CREATE TABLE form_endpoints (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  workflow_id     uuid NOT NULL,
  environment     text NOT NULL,
  public_id       text NOT NULL UNIQUE CHECK (public_id ~ '^[a-z0-9]{12}$'),
  version         int NOT NULL,
  -- The deployed version's form, copied at deploy (trigger, inputs, types):
  -- the form host reads this, never workflow definitions.
  form            jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'live' CHECK (status IN ('live', 'paused', 'closed', 'suspended')),
  opens_at        timestamptz,
  closes_at       timestamptz,
  max_submissions int CHECK (max_submissions > 0),
  submissions     int NOT NULL DEFAULT 0,
  closed_message  text CHECK (length(closed_message) <= 300),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, environment),
  FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id)
);
ALTER TABLE form_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_endpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_endpoints TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON form_endpoints TO taskiem_app;
CREATE POLICY dispatch ON form_endpoints TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON form_endpoints TO taskiem_dispatch;

-- A form step waiting: its id is the step's signal correlation. The api
-- role sends the link (token_hash is set then; the token itself is
-- derived from the public key and never stored) and the reminders; the
-- public view of the prefill and the recipient's kind are sealed for the
-- public host in public_view.
CREATE TABLE form_requests (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  run_id          uuid NOT NULL,
  step_id         text NOT NULL,
  environment     text NOT NULL,
  recipient       jsonb NOT NULL,       -- {phone | email | role}, personal values sealed
  title           text NOT NULL,
  description     text,
  schema          jsonb NOT NULL,
  prefill         jsonb,                -- values as the run had them (sealed where personal)
  via             text[] NOT NULL DEFAULT '{sms,email}',
  verify          boolean NOT NULL DEFAULT false,
  remind          interval[] NOT NULL DEFAULT '{}',
  reminders_sent  int NOT NULL DEFAULT 0,
  token_hash      bytea UNIQUE CHECK (length(token_hash) = 32),
  public_view     bytea CHECK (length(public_view) <= 65536),
  sent_at         timestamptz,
  expires_at      timestamptz NOT NULL,
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered', 'cancelled', 'expired')),
  note            text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  answered_at     timestamptz
);
CREATE INDEX form_requests_unsent ON form_requests (created_at) WHERE status = 'pending';
CREATE INDEX form_requests_run ON form_requests (run_id);
ALTER TABLE form_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_requests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON form_requests TO taskiem_app;
CREATE POLICY dispatch ON form_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON form_requests TO taskiem_dispatch;

-- One-time links to a link form, minted by the organisation.
CREATE TABLE form_links (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  endpoint_id   uuid NOT NULL REFERENCES form_endpoints(id) ON DELETE CASCADE,
  token_hash    bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  bound         jsonb,                  -- {phone | email}, sealed: the link also asks for the code sent there
  bound_view    bytea,                  -- the bound address's kind and hint, sealed for the public host
  expires_at    timestamptz NOT NULL,
  used_at       timestamptz,
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE form_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON form_links TO taskiem_app;
CREATE POLICY dispatch ON form_links TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON form_links TO taskiem_dispatch;

-- What the public role writes (platform data): submissions, their files,
-- and requests for verification codes. The api role claims and deletes.
CREATE TABLE form_inbox (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  endpoint_id    uuid REFERENCES form_endpoints(id) ON DELETE CASCADE,
  request_id     uuid REFERENCES form_requests(id) ON DELETE CASCADE,
  link_id        uuid REFERENCES form_links(id) ON DELETE SET NULL,
  reference      text NOT NULL UNIQUE,
  idem           bytea NOT NULL UNIQUE CHECK (length(idem) = 32),
  status_hash    bytea NOT NULL CHECK (length(status_hash) = 32),
  payload        bytea NOT NULL CHECK (length(payload) <= 300000),
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz,
  CHECK ((endpoint_id IS NULL) <> (request_id IS NULL))
);
CREATE TABLE form_uploads (
  id             uuid PRIMARY KEY,
  inbox_id       uuid NOT NULL REFERENCES form_inbox(id) ON DELETE CASCADE,
  field          text NOT NULL,
  data           bytea NOT NULL CHECK (length(data) <= 26300000), -- sealed; a file is at most 25 MB
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE form_codes (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  scope          uuid NOT NULL,         -- the endpoint, link or request it verifies for
  address_hash   bytea CHECK (length(address_hash) = 32),
  code_hash      bytea NOT NULL CHECK (length(code_hash) = 32),
  payload        bytea,                 -- sealed {channel, address, code} for the sender, cleared once queued
  attempts       int NOT NULL DEFAULT 0,
  expires_at     timestamptz NOT NULL,
  verified_at    timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX form_codes_scope ON form_codes (scope, address_hash, created_at);
CREATE INDEX form_codes_unsent ON form_codes (created_at) WHERE payload IS NOT NULL;
-- once_per: a verified phone, email or member's submission to an endpoint.
CREATE TABLE form_once (
  endpoint_id    uuid NOT NULL REFERENCES form_endpoints(id) ON DELETE CASCADE,
  respondent     bytea NOT NULL CHECK (length(respondent) = 32),
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (endpoint_id, respondent)
);
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['form_inbox', 'form_uploads', 'form_codes', 'form_once'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- What was submitted and what came of it, for the organisation.
CREATE TABLE form_submissions (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  endpoint_id    uuid REFERENCES form_endpoints(id) ON DELETE SET NULL,
  request_id     uuid REFERENCES form_requests(id) ON DELETE SET NULL,
  reference      text NOT NULL UNIQUE,
  status_hash    bytea NOT NULL CHECK (length(status_hash) = 32),
  answers        jsonb NOT NULL,       -- personal fields sealed
  respondent     jsonb NOT NULL,       -- {kind, phone, email, member_id}, sealed where personal
  channel        text NOT NULL CHECK (channel IN ('direct', 'embed', 'app')),
  run_id         uuid,
  status         text NOT NULL DEFAULT 'received' CHECK (status IN ('received', 'started', 'answered', 'refused')),
  -- The latest message the workflow set for the respondent (form@1
  -- respond), shown on the result page and the status link.
  message        text CHECK (length(message) <= 1000),
  note           text,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX form_submissions_list ON form_submissions (tenant_id, created_at DESC);
CREATE INDEX form_submissions_run ON form_submissions (run_id);
ALTER TABLE form_submissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_submissions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_submissions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON form_submissions TO taskiem_app;
CREATE POLICY dispatch ON form_submissions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (reference, status_hash, status, message, created_at, endpoint_id, request_id) ON form_submissions TO taskiem_dispatch;

-- Files received, kept in the organisation's Xenibase storage.
CREATE TABLE form_files (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  submission_id  uuid NOT NULL REFERENCES form_submissions(id) ON DELETE CASCADE,
  field          text NOT NULL,
  name           text NOT NULL,
  type           text NOT NULL,
  size           bigint NOT NULL,
  sha256         bytea NOT NULL CHECK (length(sha256) = 32),
  storage_key    text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE form_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON form_files TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON form_files TO taskiem_app;

-- +goose StatementBegin
-- A live form for the form host: its definition's form parts, its
-- organisation's name and id (for the websites that may frame it), and
-- whether it takes submissions now.
CREATE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types')
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A one-time link: the form it opens and whether it is still good.
CREATE FUNCTION taskiem_public_form_link(p_token_hash bytea)
RETURNS TABLE (link_id uuid, public_id text, bound_view bytea, usable boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.id, e.public_id, l.bound_view, l.used_at IS NULL AND l.expires_at > now()
    FROM form_links l JOIN form_endpoints e ON e.id = l.endpoint_id
   WHERE l.token_hash = p_token_hash
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A form step's request behind its link.
CREATE FUNCTION taskiem_public_form_request(p_token_hash bytea)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('request_id', r.id, 'title', r.title, 'description', r.description, 'schema', r.schema,
           'verify', r.verify, 'status', CASE WHEN r.status = 'pending' AND r.expires_at <= now() THEN 'expired' ELSE r.status END,
           'business', t.name, 'public_view', encode(r.public_view, 'base64'), 'expires_at', r.expires_at)
    FROM form_requests r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.token_hash = p_token_hash
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A submission: to a live, open endpoint (through an unused link when it
-- is a link form), or to a pending request. A repeat of p_idem answers
-- with its first reference; once_per refuses a second from the same
-- respondent (23505, constraint form_once_pkey). Returns NULL when the
-- form takes nothing now.
CREATE FUNCTION taskiem_public_form_submit(p_endpoint uuid, p_request uuid, p_link uuid, p_once bytea, p_reference text, p_idem bytea,
  p_status_hash bytea, p_payload bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  inbox uuid;
BEGIN
  SELECT id INTO inbox FROM form_inbox WHERE idem = p_idem;
  IF inbox IS NOT NULL THEN
    RETURN inbox;
  END IF;
  IF EXISTS (SELECT 1 FROM form_submissions s WHERE s.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'form_inbox_reference_key';
  END IF;
  IF p_endpoint IS NOT NULL THEN
    UPDATE form_endpoints e SET submissions = submissions + 1, updated_at = now()
     WHERE e.id = p_endpoint AND e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
       AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions)
       AND EXISTS (SELECT 1 FROM tenants tn WHERE tn.id = e.tenant_id AND tn.status = 'active')
    RETURNING e.tenant_id INTO t;
    IF t IS NULL THEN
      RETURN NULL;
    END IF;
    IF p_link IS NOT NULL THEN
      UPDATE form_links SET used_at = now() WHERE id = p_link AND endpoint_id = p_endpoint AND used_at IS NULL AND expires_at > now();
      IF NOT FOUND THEN
        RAISE EXCEPTION 'link used' USING ERRCODE = '23505', CONSTRAINT = 'form_links_used';
      END IF;
    END IF;
    IF p_once IS NOT NULL THEN
      INSERT INTO form_once (endpoint_id, respondent) VALUES (p_endpoint, p_once);
    END IF;
  ELSE
    SELECT r.tenant_id INTO t FROM form_requests r
     WHERE r.id = p_request AND r.status = 'pending' AND r.expires_at > now() FOR UPDATE;
    IF t IS NULL THEN
      RETURN NULL;
    END IF;
    IF EXISTS (SELECT 1 FROM form_inbox i WHERE i.request_id = p_request) THEN
      RAISE EXCEPTION 'answered' USING ERRCODE = '23505', CONSTRAINT = 'form_requests_answered';
    END IF;
  END IF;
  inbox := gen_random_uuid();
  INSERT INTO form_inbox (id, tenant_id, endpoint_id, request_id, link_id, reference, idem, status_hash, payload)
    VALUES (inbox, t, p_endpoint, p_request, p_link, p_reference, p_idem, p_status_hash, p_payload);
  RETURN inbox;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A file of a submission just written (sealed by the public host).
CREATE FUNCTION taskiem_public_form_upload(p_inbox uuid, p_field text, p_data bytea)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF (SELECT count(*) FROM form_uploads WHERE inbox_id = p_inbox) >= 10 THEN
    RAISE EXCEPTION 'too many files' USING ERRCODE = '54000';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM form_inbox WHERE id = p_inbox AND claimed_until IS NULL AND created_at > now() - interval '5 minutes') THEN
    RAISE EXCEPTION 'no such submission' USING ERRCODE = '42704';
  END IF;
  -- clock_timestamp, not now(): a submission's files arrive in one
  -- transaction, and the api role reads them back in this order.
  INSERT INTO form_uploads (id, inbox_id, field, data, created_at) VALUES (gen_random_uuid(), p_inbox, p_field, p_data, clock_timestamp());
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A verification code for a scope (endpoint, link or request) and an
-- address: at most 3 in 10 minutes for one address, 20 for one scope.
-- The api role sends it from payload. Returns false when limited.
CREATE FUNCTION taskiem_public_form_code(p_scope uuid, p_address_hash bytea, p_code_hash bytea, p_payload bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
BEGIN
  SELECT x.tenant_id INTO t FROM (
    SELECT e.tenant_id FROM form_endpoints e WHERE e.id = p_scope AND e.status = 'live'
    UNION ALL SELECT l.tenant_id FROM form_links l WHERE l.id = p_scope AND l.used_at IS NULL AND l.expires_at > now()
    UNION ALL SELECT r.tenant_id FROM form_requests r WHERE r.id = p_scope AND r.status = 'pending' AND r.expires_at > now()) x LIMIT 1;
  IF t IS NULL THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM form_codes c WHERE c.scope = p_scope AND c.address_hash IS NOT DISTINCT FROM p_address_hash AND c.created_at > now() - interval '10 minutes') >= 3
     OR (SELECT count(*) FROM form_codes c WHERE c.scope = p_scope AND c.created_at > now() - interval '10 minutes') >= 20 THEN
    RETURN false;
  END IF;
  INSERT INTO form_codes (id, tenant_id, scope, address_hash, code_hash, payload, expires_at)
    VALUES (gen_random_uuid(), t, p_scope, p_address_hash, p_code_hash, p_payload, now() + interval '10 minutes');
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Checks a code: five attempts on the newest code for the scope and
-- address, valid 10 minutes.
CREATE FUNCTION taskiem_public_form_code_check(p_scope uuid, p_address_hash bytea, p_code_hash bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c record;
BEGIN
  SELECT * INTO c FROM form_codes x WHERE x.scope = p_scope AND x.address_hash IS NOT DISTINCT FROM p_address_hash
   ORDER BY x.created_at DESC LIMIT 1 FOR UPDATE;
  IF c.id IS NULL OR c.expires_at <= now() OR c.attempts >= 5 OR c.verified_at IS NOT NULL THEN
    RETURN false;
  END IF;
  IF c.code_hash <> p_code_hash THEN
    UPDATE form_codes SET attempts = attempts + 1 WHERE id = c.id;
    RETURN false;
  END IF;
  UPDATE form_codes SET verified_at = now() WHERE id = c.id;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A submission's status for its status link: the workflow's message.
CREATE FUNCTION taskiem_public_form_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, message text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.message, s.created_at FROM form_submissions s WHERE s.reference = p_reference AND s.status_hash = p_status_hash
  UNION ALL
  SELECT 'received', NULL, i.created_at FROM form_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM form_submissions s WHERE s.reference = p_reference)
$$;
-- +goose StatementEnd

-- The api role's side.
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, endpoint_id uuid, request_id uuid, link_id uuid, reference text, status_hash bytea, payload bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM form_inbox x WHERE (x.claimed_until IS NULL AND x.created_at < now() - interval '2 seconds') OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.endpoint_id, i.request_id, i.link_id, i.reference, i.status_hash, i.payload, i.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_uploads(p_inbox uuid)
RETURNS TABLE (field text, data bytea)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.field, u.data FROM form_uploads u WHERE u.inbox_id = p_inbox ORDER BY u.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_inbox_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM form_inbox WHERE id = p_id
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- Codes to send: claimed by clearing their payload.
CREATE FUNCTION taskiem_form_codes_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, scope uuid, payload bytea)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (SELECT x.id FROM form_codes x WHERE x.payload IS NOT NULL ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED),
       old AS (SELECT x.id, x.tenant_id, x.scope, x.payload FROM form_codes x JOIN c ON c.id = x.id)
  UPDATE form_codes f SET payload = NULL FROM old WHERE f.id = old.id
  RETURNING old.id, old.tenant_id, old.scope, old.payload
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- Tenants with form requests to send or remind, and old codes forgotten.
CREATE FUNCTION taskiem_form_request_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM form_codes WHERE created_at < now() - interval '1 day';
  SELECT DISTINCT r.tenant_id FROM form_requests r WHERE r.status = 'pending'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_public_form(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_link(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_request(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_submit(uuid, uuid, uuid, bytea, text, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_upload(uuid, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_code(uuid, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_code_check(uuid, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_status(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_inbox_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_uploads(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_inbox_done(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_codes_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_request_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form(text), taskiem_public_form_link(bytea), taskiem_public_form_request(bytea),
  taskiem_public_form_submit(uuid, uuid, uuid, bytea, text, bytea, bytea, bytea), taskiem_public_form_upload(uuid, text, bytea),
  taskiem_public_form_code(uuid, bytea, bytea, bytea), taskiem_public_form_code_check(uuid, bytea, bytea), taskiem_public_form_status(text, bytea),
  taskiem_form_inbox_claim(int), taskiem_form_uploads(uuid), taskiem_form_inbox_done(uuid), taskiem_form_codes_claim(int),
  taskiem_form_request_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form(text), taskiem_public_form_link(bytea), taskiem_public_form_request(bytea),
  taskiem_public_form_submit(uuid, uuid, uuid, bytea, text, bytea, bytea, bytea), taskiem_public_form_upload(uuid, text, bytea),
  taskiem_public_form_code(uuid, bytea, bytea, bytea), taskiem_public_form_code_check(uuid, bytea, bytea), taskiem_public_form_status(text, bytea)
  TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_form_inbox_claim(int), taskiem_form_uploads(uuid), taskiem_form_inbox_done(uuid), taskiem_form_codes_claim(int),
  taskiem_form_request_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_form_request_tenants();
DROP FUNCTION taskiem_form_codes_claim(int);
DROP FUNCTION taskiem_form_inbox_done(uuid);
DROP FUNCTION taskiem_form_uploads(uuid);
DROP FUNCTION taskiem_form_inbox_claim(int);
DROP FUNCTION taskiem_public_form_status(text, bytea);
DROP FUNCTION taskiem_public_form_code_check(uuid, bytea, bytea);
DROP FUNCTION taskiem_public_form_code(uuid, bytea, bytea, bytea);
DROP FUNCTION taskiem_public_form_upload(uuid, text, bytea);
DROP FUNCTION taskiem_public_form_submit(uuid, uuid, uuid, bytea, text, bytea, bytea, bytea);
DROP FUNCTION taskiem_public_form_request(bytea);
DROP FUNCTION taskiem_public_form_link(bytea);
DROP FUNCTION taskiem_public_form(text);
DROP TABLE form_files;
DROP TABLE form_submissions;
DROP TABLE form_once;
DROP TABLE form_codes;
DROP TABLE form_uploads;
DROP TABLE form_inbox;
DROP TABLE form_links;
DROP TABLE form_requests;
DROP TABLE form_endpoints;
DELETE FROM triggers WHERE type = 'form';
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront'));
