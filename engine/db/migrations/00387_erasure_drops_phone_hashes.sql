-- Erasure leaves no keyed hash of the phone behind. reach_contacts,
-- riders and suppliers keep phone_hash = HMAC(platform key, E.164) beside
-- the sealed phone, and request links keep the hashes of the phones typed
-- in a submission. The key is one platform-wide value and Nigerian mobile
-- numbers are few, so whoever holds it and a copy of the database could
-- recover an erased person's number by trying them all.
-- Erasing a subject (subject_keys.shredded_at set) now, in the same
-- transaction:
-- - deletes the business's reach contacts whose sealed phone is the
--   subject's (their channels cascade), and takes their hashes off its
--   request links (a link left with none is deleted);
-- - gives the business's riders and suppliers with that phone a random
--   hash, so their rows keep their history but match no number;
-- - drops what the rider said to the platform number (rider_presence,
--   kept by hash across businesses) once no rider anywhere has the hash.
-- The trigger function is owned by taskiem_dispatch, which reads only the
-- columns granted here. The indexes it looks up by are 00388's.
-- +goose Up
GRANT DELETE ON reach_contacts, reach_request_links TO taskiem_dispatch;
CREATE POLICY dispatch_erased ON riders FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch_erased ON suppliers FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (phone), UPDATE (phone_hash) ON riders, suppliers TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_phone_hashes_erased() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_hashes bytea[];
BEGIN
  SELECT array_agg(DISTINCT h) INTO v_hashes FROM (
    SELECT c.phone_hash AS h FROM reach_contacts c WHERE c.tenant_id = NEW.tenant_id AND c.phone->>'subject' = NEW.subject_id
    UNION ALL SELECT r.phone_hash FROM riders r WHERE r.tenant_id = NEW.tenant_id AND r.phone->>'subject' = NEW.subject_id
    UNION ALL SELECT s.phone_hash FROM suppliers s WHERE s.tenant_id = NEW.tenant_id AND s.phone->>'subject' = NEW.subject_id) x;
  IF v_hashes IS NULL THEN
    RETURN NEW;
  END IF;
  DELETE FROM reach_contacts WHERE tenant_id = NEW.tenant_id AND phone->>'subject' = NEW.subject_id;
  UPDATE riders SET phone_hash = sha256(gen_random_uuid()::text::bytea)
   WHERE tenant_id = NEW.tenant_id AND phone->>'subject' = NEW.subject_id;
  UPDATE suppliers SET phone_hash = sha256(gen_random_uuid()::text::bytea)
   WHERE tenant_id = NEW.tenant_id AND phone->>'subject' = NEW.subject_id;
  UPDATE reach_request_links l
     SET phone_hashes = ARRAY(SELECT h FROM unnest(l.phone_hashes) h WHERE h <> ALL (v_hashes))
   WHERE l.tenant_id = NEW.tenant_id AND l.phone_hashes && v_hashes;
  DELETE FROM reach_request_links WHERE tenant_id = NEW.tenant_id AND cardinality(phone_hashes) = 0;
  DELETE FROM rider_presence p WHERE p.phone_hash = ANY (v_hashes)
     AND NOT EXISTS (SELECT 1 FROM riders r WHERE r.phone_hash = p.phone_hash);
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_phone_hashes_erased() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_phone_hashes_erased() FROM PUBLIC;
CREATE TRIGGER phone_hashes_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_phone_hashes_erased();

-- +goose Down
DROP TRIGGER phone_hashes_erased ON subject_keys;
DROP FUNCTION taskiem_phone_hashes_erased();
REVOKE SELECT (phone), UPDATE (phone_hash) ON riders, suppliers FROM taskiem_dispatch;
DROP POLICY dispatch_erased ON suppliers;
DROP POLICY dispatch_erased ON riders;
REVOKE DELETE ON reach_contacts, reach_request_links FROM taskiem_dispatch;
