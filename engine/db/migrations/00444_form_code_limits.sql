-- Codes for a verified public form (forms 7.3). A form took at most 20
-- codes in 10 minutes from every address together, and nothing else was
-- counted but the address on that form: two or three connections asking
-- for codes to random numbers spent the 20, every genuine respondent was
-- then told "too many codes", and 20 strangers were texted; for ever.
-- Now a code request also names who asks (a keyed hash of the client
-- address, as the form host's limits use) and the number or email across
-- every form (a keyed hash under one scope):
--   * one address on one form: 3 in 10 minutes, as before;
--   * one number or email, across every form: 6 a day;
--   * one asker, across every form: 3 in 10 minutes and 10 a day;
--   * one form: codes not yet confirmed in the last 10 minutes are counted
--     against 20 plus twice the codes confirmed there in the last hour, so
--     a busy form's ceiling grows with its real use. Over it the form is
--     "pressed", not locked: someone who has not asked there in the last
--     10 minutes still gets a code (and the form host alerts the owners);
--     an asker with a code waiting there is told to wait. Only at three
--     times the ceiling is everyone refused.
-- The old four-argument function stays for the form host's old replicas
-- during a deploy: it names no asker, so it gets the form's ceiling with
-- nothing past it, as before.
-- +goose Up
ALTER TABLE form_codes ADD COLUMN asker_hash bytea CHECK (length(asker_hash) = 32),
  ADD COLUMN number_hash bytea CHECK (length(number_hash) = 32);
CREATE INDEX form_codes_asker ON form_codes (asker_hash, created_at) WHERE asker_hash IS NOT NULL;
CREATE INDEX form_codes_number ON form_codes (number_hash, created_at) WHERE number_hash IS NOT NULL;

-- +goose StatementBegin
-- Asks for a code. Returns ok; pressed (sent, and the form is over its
-- ceiling: its owners should hear); or why not: gone (no such live form,
-- link or request), address, number, asker, busy.
CREATE FUNCTION taskiem_public_form_code_ask(p_scope uuid, p_address_hash bytea, p_code_hash bytea, p_payload bytea, p_asker bytea, p_number_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  waiting int;
  top int;
  pressed boolean;
BEGIN
  SELECT x.tenant_id INTO t FROM (
    SELECT e.tenant_id FROM form_endpoints e WHERE e.id = p_scope AND e.status = 'live'
    UNION ALL SELECT l.tenant_id FROM form_links l WHERE l.id = p_scope AND l.used_at IS NULL AND l.expires_at > now()
    UNION ALL SELECT r.tenant_id FROM form_requests r WHERE r.id = p_scope AND r.status = 'pending' AND r.expires_at > now()) x LIMIT 1;
  IF t IS NULL THEN
    RETURN 'gone';
  END IF;
  IF (SELECT count(*) FROM form_codes c WHERE c.scope = p_scope AND c.address_hash IS NOT DISTINCT FROM p_address_hash AND c.created_at > now() - interval '10 minutes') >= 3 THEN
    RETURN 'address';
  END IF;
  IF p_number_hash IS NOT NULL AND (SELECT count(*) FROM form_codes c WHERE c.number_hash = p_number_hash AND c.created_at > now() - interval '1 day') >= 6 THEN
    RETURN 'number';
  END IF;
  IF p_asker IS NOT NULL AND (
       (SELECT count(*) FROM form_codes c WHERE c.asker_hash = p_asker AND c.created_at > now() - interval '10 minutes') >= 3
       OR (SELECT count(*) FROM form_codes c WHERE c.asker_hash = p_asker AND c.created_at > now() - interval '1 day') >= 10) THEN
    RETURN 'asker';
  END IF;
  SELECT count(*) INTO waiting FROM form_codes c WHERE c.scope = p_scope AND c.created_at > now() - interval '10 minutes' AND c.verified_at IS NULL;
  SELECT 20 + 2 * count(*) INTO top FROM form_codes c WHERE c.scope = p_scope AND c.verified_at > now() - interval '1 hour';
  pressed := waiting >= top;
  IF pressed AND (p_asker IS NULL OR waiting >= 3 * top OR EXISTS (
       SELECT 1 FROM form_codes c WHERE c.scope = p_scope AND c.asker_hash = p_asker AND c.created_at > now() - interval '10 minutes' AND c.verified_at IS NULL)) THEN
    RETURN 'busy';
  END IF;
  INSERT INTO form_codes (id, tenant_id, scope, address_hash, code_hash, payload, expires_at, asker_hash, number_hash)
    VALUES (gen_random_uuid(), t, p_scope, p_address_hash, p_code_hash, p_payload, now() + interval '10 minutes', p_asker, p_number_hash);
  RETURN CASE WHEN pressed THEN 'pressed' ELSE 'ok' END;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_code_ask(uuid, bytea, bytea, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_code_ask(uuid, bytea, bytea, bytea, bytea, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_code_ask(uuid, bytea, bytea, bytea, bytea, bytea) TO taskiem_public;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form_code(p_scope uuid, p_address_hash bytea, p_code_hash bytea, p_payload bytea)
RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_public_form_code_ask(p_scope, p_address_hash, p_code_hash, p_payload, NULL, NULL) IN ('ok', 'pressed')
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form_code(p_scope uuid, p_address_hash bytea, p_code_hash bytea, p_payload bytea)
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
DROP FUNCTION taskiem_public_form_code_ask(uuid, bytea, bytea, bytea, bytea, bytea);
DROP INDEX form_codes_number;
DROP INDEX form_codes_asker;
ALTER TABLE form_codes DROP COLUMN number_hash, DROP COLUMN asker_hash;
