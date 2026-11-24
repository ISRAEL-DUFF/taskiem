-- Customers' conversations (docs/spec/chat-five-more-flows.md, flow 1
-- step 1, phase 1): a person who is not on the business's team, a rider
-- or a linked business, writing to the business's own WhatsApp number, is
-- a customer, and what they write is a thread in the chat, one per
-- customer number per business. Staff who handle orders read it and reply
-- there; the reply goes from the business's number.
--
-- The customer's number and every message are sealed under the
-- customer's own subject (the phone's, as their invoices are), so erasing
-- the customer erases the thread. STOP deletes the thread's messages and
-- marks it stopped: nothing more is sent to them until they send START.
-- +goose Up
CREATE TABLE customer_conversations (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  -- The business's own number the customer wrote to.
  phone_number_id  text NOT NULL CHECK (length(phone_number_id) BETWEEN 1 AND 64),
  -- The customer's subject (the phone's, pii): the thread's key, and
  -- whom it is erased with.
  subject_id       text NOT NULL CHECK (length(subject_id) BETWEEN 1 AND 200),
  -- {number, name}: the number to reply to and the name WhatsApp gave,
  -- sealed under the subject.
  who              jsonb NOT NULL CHECK (jsonb_typeof(who) = 'object'),
  -- When the customer last wrote: replies go as free text for 24 hours
  -- after it, as an approved template after that.
  last_inbound_at  timestamptz,
  last_message_at  timestamptz NOT NULL DEFAULT now(),
  -- STOP: nothing is sent until START.
  stopped_at       timestamptz,
  archived         boolean NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, phone_number_id, subject_id)
);
CREATE INDEX customer_conversations_list ON customer_conversations (tenant_id, last_message_at DESC);
ALTER TABLE customer_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_conversations TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON customer_conversations TO taskiem_app;

CREATE TABLE customer_messages (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  conversation_id  uuid NOT NULL REFERENCES customer_conversations(id),
  -- From the business (a member's reply) or from the customer.
  mine             boolean NOT NULL,
  -- text: words; media: a photo, voice note, file or location the
  -- customer sent (shown as what it was; phase 1 keeps no copy).
  kind             text NOT NULL CHECK (kind IN ('text', 'media')),
  -- The words, sealed under the customer's subject.
  body             jsonb NOT NULL,
  -- The member who replied (mine only), and their first name.
  sender_id        text CHECK (length(sender_id) <= 200),
  sender_name      text NOT NULL DEFAULT '' CHECK (length(sender_name) <= 80),
  -- A reply: sent as free text in the window, or as the approved
  -- template after it; failed when WhatsApp refused it.
  sent_as          text CHECK (sent_as IN ('text', 'template')),
  -- The chat app's id of the message (sent or received).
  wa_id            text CHECK (length(wa_id) <= 200),
  -- The member's own key for a reply, so a retried send is one message:
  -- the reply is written (sending) before it goes, so two sends of one
  -- key cannot both go.
  client_id        text CHECK (length(client_id) <= 80),
  -- A reply's way to the customer, as WhatsApp reports it: sending
  -- (written, not yet taken), sent, delivered, read, or failed (with
  -- WhatsApp's reason).
  delivery         text CHECK (delivery IN ('sending', 'sent', 'delivered', 'read', 'failed')),
  failure          text CHECK (length(failure) <= 300),
  -- Kept a year, then deleted (taskiem_customer_messages_purge); a
  -- customer's STOP or erasure deletes them sooner.
  expires_at       timestamptz NOT NULL DEFAULT now() + interval '365 days',
  -- On the customer's message, when the business read it.
  read_at          timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_messages_stream ON customer_messages (tenant_id, conversation_id, created_at DESC);
CREATE UNIQUE INDEX customer_messages_client ON customer_messages (tenant_id, conversation_id, client_id) WHERE client_id IS NOT NULL;
CREATE UNIQUE INDEX customer_messages_wa ON customer_messages (tenant_id, wa_id) WHERE wa_id IS NOT NULL AND NOT mine;
CREATE INDEX customer_messages_sent ON customer_messages (tenant_id, wa_id) WHERE wa_id IS NOT NULL AND mine;
ALTER TABLE customer_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_messages TO taskiem_app;

-- Retention across businesses: the dispatch role sees only when rows
-- expire and deletes those past it, as the Taskiem chat history's.
CREATE POLICY dispatch_purge ON customer_messages FOR DELETE TO taskiem_dispatch USING (expires_at <= now());
CREATE POLICY dispatch_read_expiry ON customer_messages FOR SELECT TO taskiem_dispatch USING (expires_at <= now());
GRANT SELECT (id, expires_at), DELETE ON customer_messages TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_messages_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM customer_messages m WHERE m.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_customer_messages_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_messages_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_messages_purge() TO taskiem_app;

-- The public menu answers a customer only when asked (its word, or a
-- choice of it under way): "menu" marks a menu shown, so the number
-- chosen from it reaches it rather than the customer's thread.
ALTER TABLE whatsapp_public_sessions DROP CONSTRAINT whatsapp_public_sessions_state_check;
ALTER TABLE whatsapp_public_sessions ADD CONSTRAINT whatsapp_public_sessions_state_check
  CHECK (state IN ('menu', 'collecting_input', 'awaiting_confirmation'));

-- +goose Down
DELETE FROM whatsapp_public_sessions WHERE state = 'menu';
ALTER TABLE whatsapp_public_sessions DROP CONSTRAINT whatsapp_public_sessions_state_check;
ALTER TABLE whatsapp_public_sessions ADD CONSTRAINT whatsapp_public_sessions_state_check
  CHECK (state IN ('collecting_input', 'awaiting_confirmation'));
DROP FUNCTION taskiem_customer_messages_purge();
DROP TABLE customer_messages;
DROP TABLE customer_conversations;
