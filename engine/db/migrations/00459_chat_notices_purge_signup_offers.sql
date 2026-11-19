-- Two review fixes to the shared number's customer words (taskiem-for-
-- everyone §9, §12, §15; stage 2):
--
-- * customer_chat_notices ("at most once per ..." for the browser chat's
--   SMS and the answer to an outsider) were never emptied: rows older than
--   two days are past every period they hold, and go in the chat history's
--   sweep (taskiem_customer_chat_notices_purge), with the sign-up offers
--   below and the sign-up codes that never led to a chat (30 days).
-- * A typed "no" is Not now only after a sign-up offer went to that address
--   recently: an outsider who receives a business's updates and writes
--   "no" to one is answered as an outsider (the chat link, or the fixed
--   answer), not told "Not now". chat_signup_offers keeps, per address
--   digest, when the offer last went; reading it takes it.
-- +goose Up
CREATE TABLE chat_signup_offers (
  address_hash  bytea PRIMARY KEY CHECK (length(address_hash) = 32),
  offered_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_signup_offers_old ON chat_signup_offers (offered_at);
ALTER TABLE chat_signup_offers ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_signup_offers FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON chat_signup_offers TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON chat_signup_offers TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON chat_signup_offers FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_signup_offers TO taskiem_dispatch;

-- The offer went to an address now.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_offered(p_hash bytea)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO chat_signup_offers (address_hash) VALUES (p_hash)
  ON CONFLICT (address_hash) DO UPDATE SET offered_at = now()
$$;
-- +goose StatementEnd

-- Whether the offer went to an address within p_within; reading it takes
-- it (a second "no" is not the offer's).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_offer_taken(p_hash bytea, p_within interval)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH d AS (DELETE FROM chat_signup_offers o WHERE o.address_hash = p_hash RETURNING o.offered_at)
  SELECT COALESCE((SELECT offered_at > now() - p_within FROM d), false)
$$;
-- +goose StatementEnd

CREATE POLICY dispatch ON customer_chat_notices TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (last_at), DELETE ON customer_chat_notices TO taskiem_dispatch;

-- The sweep: notices past every period (two days), sign-up offers past a
-- day, and sign-up codes no chat settled in 30 days. Answers how many went.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_chat_notices_purge()
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH n AS (DELETE FROM customer_chat_notices c WHERE c.last_at < now() - interval '2 days' RETURNING 1),
       o AS (DELETE FROM chat_signup_offers c WHERE c.offered_at < now() - interval '1 day' RETURNING 1),
       s AS (DELETE FROM customer_signup_sources c WHERE c.settled_at IS NULL AND c.created_at < now() - interval '30 days' RETURNING 1)
  SELECT (SELECT count(*) FROM n) + (SELECT count(*) FROM o) + (SELECT count(*) FROM s)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_signup_offered(bytea)', 'taskiem_chat_signup_offer_taken(bytea, interval)',
    'taskiem_customer_chat_notices_purge()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_chat_notices_purge();
REVOKE SELECT (last_at), DELETE ON customer_chat_notices FROM taskiem_dispatch;
DROP POLICY dispatch ON customer_chat_notices;
DROP FUNCTION taskiem_chat_signup_offer_taken(bytea, interval);
DROP FUNCTION taskiem_chat_signup_offered(bytea);
DROP TABLE chat_signup_offers;
