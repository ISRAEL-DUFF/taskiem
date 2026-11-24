-- Business chat tools: Book (docs/spec/business-chat-tools.md §3, §6 item 3).
-- A linked business books one of the other's services from their
-- conversation, through the storefront's booking: the slot is held and
-- the booking goes into the seller's storefront inbox, as one from the
-- storefront's page would, and is recorded by the same worker.
-- +goose Up

-- The buyer's bookings with a linked business, by the Idempotency-Key
-- that made each: a repeat answers with the first booking.
CREATE TABLE link_bookings (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  link_id       uuid NOT NULL REFERENCES business_links(id),
  idem_key      text NOT NULL CHECK (length(idem_key) BETWEEN 1 AND 255),
  reference     text NOT NULL,
  service_id    text NOT NULL,
  service_name  text NOT NULL,
  starts_at     timestamptz NOT NULL,
  ends_at       timestamptz NOT NULL,
  price_kobo    bigint,
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, idem_key)
);
CREATE INDEX link_bookings_starts ON link_bookings (tenant_id, link_id, starts_at);
ALTER TABLE link_bookings ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_bookings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_bookings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON link_bookings TO taskiem_app;

-- A booking from a linked business, called in the seller's scope (through
-- the door, links.EnterTx): the booking action of the business in scope,
-- enabled, its front enabled, the storefront neither closed nor suspended
-- nor its orders paused; published or not (§7.1). Holds the slot and
-- writes the sealed submission in one transaction, as
-- taskiem_public_book_action does. NULL when bookings are not taken; a
-- slot no longer free raises 23P01; a reference taken raises 23505.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_book(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  t uuid;
  sf uuid;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem AND tenant_id = v_me;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference)
     OR EXISTS (SELECT 1 FROM storefront_inbox i WHERE i.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id, s.id INTO t, sf FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status NOT IN ('closed', 'suspended') AND s.orders_paused IS NULL
   WHERE a.id = p_action AND a.enabled AND a.kind = 'booking' AND s.tenant_id = v_me;
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_until <= p_start OR p_start < now() THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  PERFORM taskiem_storefront_lock(sf);
  IF NOT taskiem_storefront_slot_free(sf, NULL, p_service, p_start, p_until, p_capacity) THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  INSERT INTO storefront_claims (reference, storefront_id, tenant_id, item_id, starts_at, until_at, expires_at)
    VALUES (p_reference, sf, t, p_service, p_start, p_until, now() + interval '10 minutes');
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, t, 'booking', p_reference, p_idem, p_status_hash, p_payload, p_action);
  RETURN p_reference;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int);
DROP TABLE link_bookings;
