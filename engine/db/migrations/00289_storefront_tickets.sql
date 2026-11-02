-- Tickets and Classes (docs/spec/fronts.md §6, F6). A ticket order's
-- seats, each with its own code (the order's reference and the seat:
-- AK-7QX4KD-2), checked in once at the door. A class order's fee may be
-- paid in instalments (the first when the place is taken), and a full
-- class may take a name for its waiting list: an order that holds no
-- place until the owner offers one.

-- +goose Up
ALTER TABLE storefront_orders ADD COLUMN instalments smallint NOT NULL DEFAULT 1 CHECK (instalments BETWEEN 1 AND 4);
ALTER TABLE storefront_orders ADD COLUMN waitlist boolean NOT NULL DEFAULT false;

CREATE TABLE storefront_tickets (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  reference      text NOT NULL,
  seat           int  NOT NULL CHECK (seat BETWEEN 1 AND 100),
  code           text NOT NULL UNIQUE,
  checked_in_at  timestamptz,
  checked_in_by  text,
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (reference, seat)
);
CREATE INDEX storefront_tickets_tenant ON storefront_tickets (tenant_id, reference);
ALTER TABLE storefront_tickets ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_tickets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_tickets TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_tickets TO taskiem_app;

-- +goose Down
DROP TABLE storefront_tickets;
ALTER TABLE storefront_orders DROP COLUMN waitlist;
ALTER TABLE storefront_orders DROP COLUMN instalments;
