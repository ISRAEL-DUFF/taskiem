-- The fingerprint as its own way to confirm a payment (docs/spec/
-- fingerprint-key.md; api/payments_fingerprint.go). Turning it on makes a
-- key pair in the phone's secure hardware, usable only after the phone's
-- fingerprint or face check; its public key is kept here for that person
-- and that phone (pay_keys). Paying with it: the server keeps a one-use
-- challenge bound to one payment and one key (pay_key_challenges), the
-- phone signs it, the server checks the signature. Like the PIN, a person's
-- keys are theirs, not a business's: reached only through the functions
-- below. A payment confirmed with it says so (confirmed_with
-- 'fingerprint'), so its own day's total can be counted across every
-- business, as the PIN's is (00525). Turning it on can be proved by a code
-- to the person's phone: its own purpose of phone_codes ('pay_key').
-- +goose Up
CREATE TABLE pay_keys (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- The app's install (devices.go deviceReq.id): one key a phone.
  device_id    uuid NOT NULL,
  device_name  text NOT NULL CHECK (length(device_name) BETWEEN 1 AND 80),
  platform     text NOT NULL CHECK (platform IN ('android', 'ios')),
  -- An ECDSA P-256 public key, DER SubjectPublicKeyInfo (91 bytes).
  public_key   bytea NOT NULL CHECK (length(public_key) BETWEEN 64 AND 200),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  UNIQUE (user_id, device_id)
);
ALTER TABLE pay_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON pay_keys TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON pay_keys TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON pay_keys FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON pay_keys TO taskiem_dispatch;

-- One challenge, one payment, one key, a short while, once.
CREATE TABLE pay_key_challenges (
  hash       bytea PRIMARY KEY CHECK (length(hash) = 32),
  key_id     uuid NOT NULL REFERENCES pay_keys(id) ON DELETE CASCADE,
  payment_id uuid NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE INDEX ON pay_key_challenges (key_id);
ALTER TABLE pay_key_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_key_challenges FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON pay_key_challenges TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON pay_key_challenges TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON pay_key_challenges FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON pay_key_challenges TO taskiem_dispatch;

-- Keeps a phone's key for a person, replacing that phone's earlier one
-- (and its challenges): the new key's id.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_key_put(p_user uuid, p_device uuid, p_name text, p_platform text, p_key bytea)
RETURNS uuid LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM pay_keys k WHERE k.user_id = p_user AND k.device_id = p_device;
  INSERT INTO pay_keys (user_id, device_id, device_name, platform, public_key) VALUES (p_user, p_device, p_name, p_platform, p_key) RETURNING id;
$$;
-- +goose StatementEnd

-- A person's keys, newest first, without the keys themselves.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_keys(p_user uuid)
RETURNS TABLE (id uuid, device_id uuid, device_name text, platform text, created_at timestamptz, last_used_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.device_id, k.device_name, k.platform, k.created_at, k.last_used_at FROM pay_keys k WHERE k.user_id = p_user ORDER BY k.created_at DESC
$$;
-- +goose StatementEnd

-- Removes one of a person's keys: its phone's name, or no row when it was
-- not theirs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_key_remove(p_user uuid, p_key uuid)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM pay_keys k WHERE k.id = p_key AND k.user_id = p_user RETURNING k.device_name
$$;
-- +goose StatementEnd

-- Keeps a challenge for one payment with one of the person's keys; false
-- when the key is not theirs. Old challenges go.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_key_challenge(p_user uuid, p_key uuid, p_payment uuid, p_hash bytea, p_ttl interval)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM pay_key_challenges c WHERE c.expires_at < now() - interval '1 hour';
  IF NOT EXISTS (SELECT 1 FROM pay_keys k WHERE k.id = p_key AND k.user_id = p_user) THEN
    RETURN false;
  END IF;
  INSERT INTO pay_key_challenges (hash, key_id, payment_id, expires_at) VALUES (p_hash, p_key, p_payment, now() + p_ttl);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Takes a challenge once, before it expires, for that payment and that
-- key of the person's: the key's public key to check the signature with,
-- or no row.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_key_take(p_user uuid, p_key uuid, p_payment uuid, p_hash bytea)
RETURNS bytea LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (DELETE FROM pay_key_challenges c WHERE c.hash = p_hash AND c.key_id = p_key AND c.payment_id = p_payment AND c.expires_at > now() RETURNING c.key_id)
  SELECT k.public_key FROM pay_keys k JOIN c ON c.key_id = k.id WHERE k.user_id = p_user
$$;
-- +goose StatementEnd

-- Records a verified use of a key.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pay_key_used(p_user uuid, p_key uuid)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE pay_keys k SET last_used_at = now() WHERE k.id = p_key AND k.user_id = p_user
$$;
-- +goose StatementEnd

-- A payment confirmed with the fingerprint, and its own day's total.
ALTER TABLE payments DROP CONSTRAINT payments_confirmed_with_check;
ALTER TABLE payments ADD CONSTRAINT payments_confirmed_with_check CHECK (confirmed_with IN ('passkey', 'pin', 'fingerprint'));
CREATE INDEX payments_fingerprint_day ON payments (confirmed_by, confirmed_at) WHERE confirmed_with = 'fingerprint';
CREATE POLICY fingerprint_day ON payments FOR SELECT TO taskiem_dispatch USING (confirmed_with = 'fingerprint');

-- What a person confirmed with their fingerprint today (Lagos's day, as
-- pay limits count it), in every business: sent, waiting, paid or unknown.
-- +goose StatementBegin
CREATE FUNCTION taskiem_fingerprint_paid_today(p_user uuid)
RETURNS bigint LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(sum(amount_kobo), 0)::bigint FROM payments
   WHERE confirmed_with = 'fingerprint' AND confirmed_by = p_user::text
     AND status IN ('sending', 'waiting_ok', 'paid', 'unknown')
     AND confirmed_at > date_trunc('day', now() AT TIME ZONE 'Africa/Lagos') AT TIME ZONE 'Africa/Lagos'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_pay_key_put(uuid, uuid, text, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pay_keys(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pay_key_remove(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pay_key_challenge(uuid, uuid, uuid, bytea, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pay_key_take(uuid, uuid, uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pay_key_used(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_fingerprint_paid_today(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pay_key_put(uuid, uuid, text, text, bytea), taskiem_pay_keys(uuid), taskiem_pay_key_remove(uuid, uuid),
  taskiem_pay_key_challenge(uuid, uuid, uuid, bytea, interval), taskiem_pay_key_take(uuid, uuid, uuid, bytea), taskiem_pay_key_used(uuid, uuid),
  taskiem_fingerprint_paid_today(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pay_key_put(uuid, uuid, text, text, bytea), taskiem_pay_keys(uuid), taskiem_pay_key_remove(uuid, uuid),
  taskiem_pay_key_challenge(uuid, uuid, uuid, bytea, interval), taskiem_pay_key_take(uuid, uuid, uuid, bytea), taskiem_pay_key_used(uuid, uuid),
  taskiem_fingerprint_paid_today(uuid) TO taskiem_app;

-- A code to the person's phone proves turning it on. The purposes already
-- allowed are kept as they are, whichever migration added them: this one
-- is added to the list, not a new list written.
-- +goose StatementBegin
DO $$
DECLARE
  def text;
BEGIN
  SELECT pg_get_constraintdef(c.oid) INTO def FROM pg_constraint c WHERE c.conrelid = 'phone_codes'::regclass AND c.conname = 'phone_codes_purpose_check';
  IF def IS NULL OR position('ARRAY[' IN def) = 0 THEN
    RAISE EXCEPTION 'phone_codes_purpose_check is not a list of purposes: %', def;
  END IF;
  ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
  EXECUTE 'ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check ' || replace(def, 'ARRAY[', 'ARRAY[''pay_key''::text, ');
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM phone_codes WHERE purpose = 'pay_key';
-- +goose StatementBegin
DO $$
DECLARE
  def text;
BEGIN
  SELECT pg_get_constraintdef(c.oid) INTO def FROM pg_constraint c WHERE c.conrelid = 'phone_codes'::regclass AND c.conname = 'phone_codes_purpose_check';
  ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
  EXECUTE 'ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check ' || replace(def, '''pay_key''::text, ', '');
END $$;
-- +goose StatementEnd
DROP FUNCTION taskiem_fingerprint_paid_today(uuid);
DROP POLICY fingerprint_day ON payments;
DROP INDEX payments_fingerprint_day;
UPDATE payments SET confirmed_with = NULL WHERE confirmed_with = 'fingerprint';
ALTER TABLE payments DROP CONSTRAINT payments_confirmed_with_check;
ALTER TABLE payments ADD CONSTRAINT payments_confirmed_with_check CHECK (confirmed_with IN ('passkey', 'pin'));
DROP FUNCTION taskiem_pay_key_used(uuid, uuid);
DROP FUNCTION taskiem_pay_key_take(uuid, uuid, uuid, bytea);
DROP FUNCTION taskiem_pay_key_challenge(uuid, uuid, uuid, bytea, interval);
DROP FUNCTION taskiem_pay_key_remove(uuid, uuid);
DROP FUNCTION taskiem_pay_keys(uuid);
DROP FUNCTION taskiem_pay_key_put(uuid, uuid, text, text, bytea);
DROP TABLE pay_key_challenges;
DROP TABLE pay_keys;
