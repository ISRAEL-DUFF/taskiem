-- Riders (docs/spec/dispatch.md D1, decision 0055): the people who deliver
-- for a business, outside it as its customers are (decision 0039). A rider
-- is the business's, keyed as its contacts are by the keyed hash of their
-- phone, with their name and phone sealed under the phone's personal-data
-- subject; they are reached on the chat app they tapped in on (their
-- contact in reach_contacts, the same phone hash) and by SMS until then.
-- A rider is retired rather than deleted, so their deliveries keep them.
--
-- A rider several businesses added is a row in each, under each one's own
-- security. Presence is the one place they are known as a person: what
-- they said to the platform number (off, busy till a time), keyed by the
-- phone hash, written and read only through functions that answer a state
-- and never a business.
--
-- The order form's delivery areas (zones) and their fees live in its
-- options (storefront_actions); an order keeps the area chosen and the fee.

-- +goose Up
CREATE TABLE riders (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  phone_hash  bytea NOT NULL CHECK (length(phone_hash) = 32),
  phone       jsonb NOT NULL,  -- sealed E.164
  name        jsonb NOT NULL,  -- sealed, under the phone's subject
  zones       text[] NOT NULL DEFAULT '{}' CHECK (cardinality(zones) <= 30),
  fee_kobo    bigint NOT NULL DEFAULT 0 CHECK (fee_kobo >= 0 AND fee_kobo <= 10000000),
  active      boolean NOT NULL DEFAULT true,
  invited_at  timestamptz,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  retired_at  timestamptz,
  UNIQUE (tenant_id, phone_hash)
);
CREATE INDEX riders_phone ON riders (phone_hash) WHERE retired_at IS NULL;
ALTER TABLE riders ENABLE ROW LEVEL SECURITY;
ALTER TABLE riders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON riders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON riders TO taskiem_app;
-- The definers below find a rider by their phone across businesses; they
-- read no name, phone, zone or fee.
CREATE POLICY dispatch ON riders FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, phone_hash, retired_at) ON riders TO taskiem_dispatch;

-- What a rider said to the platform number, for every business they ride
-- for: off (until they say on), or busy until a time. No row: free.
CREATE TABLE rider_presence (
  phone_hash  bytea PRIMARY KEY CHECK (length(phone_hash) = 32),
  said        text NOT NULL CHECK (said IN ('off', 'busy')),
  until       timestamptz,
  said_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (said <> 'busy' OR until IS NOT NULL)
);
ALTER TABLE rider_presence ENABLE ROW LEVEL SECURITY;
ALTER TABLE rider_presence FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON rider_presence TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON rider_presence TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON rider_presence FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON rider_presence TO taskiem_dispatch;

-- +goose StatementBegin
-- A rider's words to the platform number: on, off, or busy until a time.
-- The rider is the phone given (WhatsApp proves the number a message comes
-- from), else the phone of the contact the chat address is linked to
-- (Telegram). Answers how many live businesses they ride for; 0 (nothing
-- written) when they ride for none.
CREATE FUNCTION taskiem_rider_say(p_phone_hash bytea, p_channel text, p_address_hash bytea, p_said text, p_until timestamptz)
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  h bytea;
  n int;
BEGIN
  IF p_said NOT IN ('on', 'off', 'busy') OR (p_said = 'busy' AND p_until IS NULL) THEN
    RAISE EXCEPTION 'taskiem_rider_say: on, off, or busy with a time';
  END IF;
  SELECT r.phone_hash, count(DISTINCT r.tenant_id) INTO h, n
    FROM riders r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.retired_at IS NULL
     AND (r.phone_hash = p_phone_hash
          OR r.phone_hash IN (SELECT c.phone_hash FROM reach_channels ch JOIN reach_contacts c ON c.id = ch.contact_id
                               WHERE ch.channel = p_channel AND ch.address_hash = p_address_hash))
   GROUP BY r.phone_hash ORDER BY 2 DESC LIMIT 1;
  IF h IS NULL THEN
    RETURN 0;
  END IF;
  IF p_said = 'on' THEN
    DELETE FROM rider_presence p WHERE p.phone_hash = h;
  ELSE
    INSERT INTO rider_presence (phone_hash, said, until) VALUES (h, p_said, CASE WHEN p_said = 'busy' THEN p_until END)
    ON CONFLICT (phone_hash) DO UPDATE SET said = EXCLUDED.said, until = EXCLUDED.until, said_at = now();
  END IF;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- What the caller's business may know of its riders' presence: for each of
-- the phones that is a rider of a business in the caller's scope, off or
-- busy until a time (no row: free). Never a business or an order.
CREATE FUNCTION taskiem_rider_presence(p_hashes bytea[])
RETURNS TABLE (phone_hash bytea, said text, until timestamptz, said_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.phone_hash, p.said, p.until, p.said_at FROM rider_presence p
   WHERE p.phone_hash = ANY (p_hashes) AND (p.said = 'off' OR p.until > now())
     AND EXISTS (SELECT 1 FROM riders r WHERE r.phone_hash = p.phone_hash AND r.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_rider_say(bytea, text, bytea, text, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_rider_presence(bytea[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_rider_say(bytea, text, bytea, text, timestamptz), taskiem_rider_presence(bytea[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_rider_say(bytea, text, bytea, text, timestamptz), taskiem_rider_presence(bytea[]) TO taskiem_app;

-- An order's delivery area and its fee (part of total_kobo).
ALTER TABLE storefront_orders
  ADD COLUMN zone text CHECK (length(zone) BETWEEN 1 AND 40),
  ADD COLUMN delivery_kobo bigint CHECK (delivery_kobo >= 0);

-- +goose Down
ALTER TABLE storefront_orders DROP COLUMN delivery_kobo, DROP COLUMN zone;
DROP FUNCTION taskiem_rider_presence(bytea[]);
DROP FUNCTION taskiem_rider_say(bytea, text, bytea, text, timestamptz);
DROP TABLE rider_presence;
DROP TABLE riders;
