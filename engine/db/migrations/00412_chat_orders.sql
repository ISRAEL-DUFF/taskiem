-- Orders from customers' messages (docs/spec/chat-five-more-flows.md,
-- flow 1, phase 2): Taskiem reads an order out of what a customer writes
-- to the business's own number and puts a draft in front of staff, on a
-- card in the customer's thread. Staff answer its questions, change it,
-- ask the customer, and confirm it; confirmed, it is a storefront order of
-- the kind chat, so payment matching, stock and dispatch work on it as on
-- an order from the page.
--
-- A draft is sealed under the customer's subject (it holds their words and
-- address), so erasing the customer erases it. It goes with the message it
-- was read from (STOP deletes the thread's messages; the purge deletes
-- them after a year); the order it became is the business's record and
-- stays, as invoices do.
-- +goose Up
-- The kind chat: like trade, it needs no storefront. On a business with
-- one, it names the storefront's built-in order action (its status page
-- and payment connection); on one without, none.
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote', 'trade', 'chat'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_trade;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_trade CHECK ((kind IN ('trade', 'chat')) OR (storefront_id IS NOT NULL AND front_id IS NOT NULL AND action_id IS NOT NULL));
-- The fill-in of 00293, with the chat kind taken as trade is.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_orders_fill() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  a uuid;
  f uuid;
BEGIN
  IF NEW.kind IN ('trade', 'chat') THEN
    IF NEW.action_id IS NULL AND NEW.storefront_id IS NOT NULL THEN
      SELECT id, front_id INTO NEW.action_id, NEW.front_id FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = 'order' AND builtin;
    ELSIF NEW.action_id IS NOT NULL AND NEW.front_id IS NULL THEN
      SELECT front_id INTO NEW.front_id FROM storefront_actions WHERE id = NEW.action_id;
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.action_id IS NULL THEN
    SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    IF a IS NULL THEN
      INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment)
        VALUES (NEW.storefront_id, NEW.kind, NEW.tenant_id, false, initcap(NEW.kind), CASE WHEN NEW.kind IN ('order', 'booking') THEN 'after_accept' ELSE 'none' END)
        ON CONFLICT (storefront_id, kind) WHERE builtin DO NOTHING;
      SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    END IF;
    NEW.action_id := a;
    NEW.front_id := COALESCE(NEW.front_id, f);
  ELSIF NEW.front_id IS NULL THEN
    SELECT front_id INTO NEW.front_id FROM storefront_actions WHERE id = NEW.action_id;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TABLE order_drafts (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  conversation_id  uuid NOT NULL REFERENCES customer_conversations(id),
  -- The customer's message it was read from (none when staff started it).
  message_id       uuid REFERENCES customer_messages(id) ON DELETE CASCADE,
  -- open: staff have it; asked: its questions went to the customer;
  -- confirmed: it is an order; dropped.
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'asked', 'confirmed', 'dropped')),
  -- The lines, questions, delivery and note (read.Draft) and what staff
  -- set (delivery fee, pay on delivery, prices they changed), sealed under
  -- the customer's subject.
  body             jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
  -- What it comes to now, lines and delivery: for lists, without opening.
  total_kobo       bigint NOT NULL DEFAULT 0 CHECK (total_kobo >= 0),
  -- The order it became.
  order_id         uuid REFERENCES storefront_orders(id),
  reference        text CHECK (reference IS NULL OR length(reference) BETWEEN 1 AND 40),
  -- 'reader' (Taskiem read it) or the member who started it; who last
  -- changed and who confirmed it.
  made_by          text NOT NULL CHECK (length(made_by) BETWEEN 1 AND 200),
  changed_by       text CHECK (changed_by IS NULL OR length(changed_by) BETWEEN 1 AND 200),
  confirmed_by     text CHECK (confirmed_by IS NULL OR length(confirmed_by) BETWEEN 1 AND 200),
  -- Each change bumps it; a change made on an older version is refused.
  version          integer NOT NULL DEFAULT 1 CHECK (version >= 1),
  asked_at         timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'confirmed') = (order_id IS NOT NULL))
);
CREATE INDEX order_drafts_thread ON order_drafts (tenant_id, conversation_id, created_at);
-- One draft being worked on per thread: a customer's answer goes to it.
CREATE UNIQUE INDEX order_drafts_open ON order_drafts (conversation_id) WHERE status IN ('open', 'asked');
CREATE INDEX order_drafts_waiting ON order_drafts (tenant_id, updated_at) WHERE status IN ('open', 'asked');
ALTER TABLE order_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_drafts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_drafts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON order_drafts TO taskiem_app;

-- +goose Down
DROP TABLE order_drafts;
DELETE FROM storefront_orders WHERE kind = 'chat';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_orders_fill() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  a uuid;
  f uuid;
BEGIN
  IF NEW.kind = 'trade' THEN
    IF NEW.action_id IS NULL AND NEW.storefront_id IS NOT NULL THEN
      SELECT id, front_id INTO NEW.action_id, NEW.front_id FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = 'order' AND builtin;
    ELSIF NEW.action_id IS NOT NULL AND NEW.front_id IS NULL THEN
      SELECT front_id INTO NEW.front_id FROM storefront_actions WHERE id = NEW.action_id;
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.action_id IS NULL THEN
    SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    IF a IS NULL THEN
      INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment)
        VALUES (NEW.storefront_id, NEW.kind, NEW.tenant_id, false, initcap(NEW.kind), CASE WHEN NEW.kind IN ('order', 'booking') THEN 'after_accept' ELSE 'none' END)
        ON CONFLICT (storefront_id, kind) WHERE builtin DO NOTHING;
      SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    END IF;
    NEW.action_id := a;
    NEW.front_id := COALESCE(NEW.front_id, f);
  ELSIF NEW.front_id IS NULL THEN
    SELECT front_id INTO NEW.front_id FROM storefront_actions WHERE id = NEW.action_id;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_trade;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_trade CHECK ((kind = 'trade') OR (storefront_id IS NOT NULL AND front_id IS NOT NULL AND action_id IS NOT NULL));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote', 'trade'));
