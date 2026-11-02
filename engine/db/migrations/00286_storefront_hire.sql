-- Hire (docs/spec/fronts.md §6, F5): chairs, canopies, generators for
-- dates. A hire action takes the dates-and-quantity field; its submission
-- claims qty of an item from one date to another, and is refused beyond
-- what is free for those dates: the item's count (what the owner has)
-- less the live claims on it that overlap. The check runs here, in one
-- transaction under the list's lock, so two customers cannot both take
-- the last canopy. A hire that is out is a claim, and Returned ends it:
-- hires make no stock movements.

-- +goose Up
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire'));
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking', 'hire'));
-- When a hire's items came back: Returned ends its claim.
ALTER TABLE storefront_orders ADD COLUMN returned_at timestamptz;

-- A claim on an action's front's list: qty of p_item from p_start to
-- p_until, held ten minutes while the submission is read, and the
-- submission written in the same transaction. NULL when the action is not
-- open; raises 23P01 (constraint storefront_claims_free) when that many
-- are not free for those times, or the item is gone or hidden.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_claim(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_item text, p_qty int, p_start timestamptz, p_until timestamptz)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  sf uuid;
  blk uuid;
  tbl text;
  k text;
  it jsonb;
  have int;
  used int;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id, s.id, b.id, b.config->>'table_name', a.kind INTO t, sf, blk, tbl, k FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
    JOIN LATERAL (SELECT x.id, x.config FROM storefront_blocks x WHERE x.front_id = f.id AND x.kind = 'list' ORDER BY x.position LIMIT 1) b ON true
   WHERE a.id = p_action AND a.enabled AND a.kind = 'hire';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_qty IS NULL OR p_qty < 1 OR p_until <= p_start OR p_start < now() - interval '1 day' THEN
    RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('storefront-claims:' || blk::text, 0));
  SELECT c.item INTO it FROM storefront_catalogue c WHERE c.storefront_id = sf AND c.table_name = tbl AND c.row_id = p_item;
  IF it IS NULL OR NOT COALESCE((it->>'available')::boolean, false) THEN
    RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
  END IF;
  have := COALESCE((it->>'capacity')::int, 0);
  SELECT COALESCE(sum(h.qty), 0) INTO used FROM storefront_claims h
   WHERE h.block_id = blk AND h.item_id = p_item AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_until AND p_start < h.until_at;
  IF have - used < p_qty THEN
    RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
  END IF;
  INSERT INTO storefront_claims (reference, storefront_id, tenant_id, item_id, qty, block_id, starts_at, until_at, expires_at)
    VALUES (p_reference, sf, t, p_item, p_qty, blk, p_start, p_until, now() + interval '10 minutes');
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, t, k, p_reference, p_idem, p_status_hash, p_payload, p_action);
  RETURN p_reference;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION taskiem_public_claim(uuid, text, bytea, bytea, bytea, text, int, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_claim(uuid, text, bytea, bytea, bytea, text, int, timestamptz, timestamptz) TO taskiem_public;

-- How many of each item of a list are taken between two times, for the
-- hire page: no references, no customers.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_taken(p_action uuid, p_from timestamptz, p_to timestamptz)
RETURNS TABLE (item_id text, starts_at timestamptz, until_at timestamptz, qty int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.item_id, h.starts_at, h.until_at, sum(h.qty)::int FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN LATERAL (SELECT x.id FROM storefront_blocks x WHERE x.front_id = f.id AND x.kind = 'list' ORDER BY x.position LIMIT 1) b ON true
    JOIN storefront_claims h ON h.block_id = b.id
   WHERE a.id = p_action AND a.enabled AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_to AND p_from < h.until_at
   GROUP BY h.item_id, h.starts_at, h.until_at
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION taskiem_public_taken(uuid, timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_taken(uuid, timestamptz, timestamptz) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_taken(uuid, timestamptz, timestamptz);
DROP FUNCTION taskiem_public_claim(uuid, text, bytea, bytea, bytea, text, int, timestamptz, timestamptz);
DELETE FROM storefront_claims WHERE reference IN (SELECT reference FROM storefront_orders WHERE kind = 'hire');
DELETE FROM storefront_orders WHERE kind = 'hire';
DELETE FROM storefront_inbox WHERE kind = 'hire';
DELETE FROM triggers WHERE storefront_action = 'hire';
DELETE FROM storefront_actions WHERE kind = 'hire';
ALTER TABLE storefront_orders DROP COLUMN returned_at;
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking'));
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking'));
