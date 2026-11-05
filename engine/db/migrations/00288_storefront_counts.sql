-- Tickets and Classes (docs/spec/fronts.md §6, F6): the count field. A
-- ticket or a place claims qty of an event or a class from its front's
-- list, refused beyond the list's count (its tickets, its places) less the
-- live claims on it; the claim has no times, so every claim on an item
-- counts against it, and it holds for as long as its order lives. Sales
-- close when the event or the class starts.

-- +goose Up
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_claim(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
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
  setup interval;
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
   WHERE a.id = p_action AND a.enabled AND a.kind IN ('hire', 'space', 'tickets', 'class');
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_qty IS NULL OR p_qty < 1 OR (k NOT IN ('tickets', 'class') AND (p_until <= p_start OR p_start < now() - interval '1 day')) THEN
    RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
  END IF;
  -- Tickets and places are counted, not timed: a claim on one holds for
  -- as long as its order lives, whatever the times passed.
  IF k IN ('tickets', 'class') THEN
    p_start := '-infinity';
    p_until := 'infinity';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('storefront-claims:' || blk::text, 0));
  SELECT c.item INTO it FROM storefront_catalogue c WHERE c.storefront_id = sf AND c.table_name = tbl AND c.row_id = p_item;
  -- An event or a class that has started sells no more.
  IF it IS NULL OR NOT COALESCE((it->>'available')::boolean, false)
     OR (k IN ('tickets', 'class') AND it->>'starts' IS NOT NULL AND (it->>'starts')::timestamptz <= now()) THEN
    RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
  END IF;
  -- A hire takes some of an item's count; a space is booked by one
  -- party at a time, with the list's setup time kept clear either side.
  setup := interval '0';
  IF k = 'space' THEN
    IF p_qty <> 1 THEN
      RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
    END IF;
    have := 1;
    SELECT make_interval(mins => LEAST(GREATEST(COALESCE((x.config->'calendar'->>'setup_minutes')::int, 0), 0), 1440)) INTO setup
      FROM storefront_blocks x WHERE x.id = blk;
  ELSE
    have := COALESCE((it->>'capacity')::int, 0);
  END IF;
  SELECT COALESCE(sum(h.qty), 0) INTO used FROM storefront_claims h
   WHERE h.block_id = blk AND h.item_id = p_item AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_until + setup AND p_start - setup < h.until_at;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_claim(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
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
  setup interval;
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
   WHERE a.id = p_action AND a.enabled AND a.kind IN ('hire', 'space');
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
  -- A hire takes some of an item's count; a space is booked by one
  -- party at a time, with the list's setup time kept clear either side.
  setup := interval '0';
  IF k = 'space' THEN
    IF p_qty <> 1 THEN
      RAISE EXCEPTION 'not free' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_claims_free';
    END IF;
    have := 1;
    SELECT make_interval(mins => LEAST(GREATEST(COALESCE((x.config->'calendar'->>'setup_minutes')::int, 0), 0), 1440)) INTO setup
      FROM storefront_blocks x WHERE x.id = blk;
  ELSE
    have := COALESCE((it->>'capacity')::int, 0);
  END IF;
  SELECT COALESCE(sum(h.qty), 0) INTO used FROM storefront_claims h
   WHERE h.block_id = blk AND h.item_id = p_item AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_until + setup AND p_start - setup < h.until_at;
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
DELETE FROM storefront_claims WHERE reference IN (SELECT reference FROM storefront_orders WHERE kind IN ('tickets', 'class'));
DELETE FROM storefront_orders WHERE kind IN ('tickets', 'class');
DELETE FROM storefront_inbox WHERE kind IN ('tickets', 'class');
DELETE FROM triggers WHERE storefront_action IN ('tickets', 'class');
DELETE FROM storefront_actions WHERE kind IN ('tickets', 'class');
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space'));
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space'));
