-- Business links, L3: the conversation between two linked businesses
-- (decision 0061; docs/spec/business-links.md §5.5, §8). Each side keeps
-- the whole conversation under its own tenancy: a message is written as
-- the sender's own copy (mine), then, through taskiem_link_enter in the
-- same transaction, as the other side's copy, the two sharing pair_id.
-- Neither side deletes the conversation: it is the record of the trade.
--
-- A card is a document event in the conversation (an order, a bill, a
-- payment, a query), written by Taskiem into each side at the moment it
-- happens, naming that side's own document. A message quoting a card is a
-- reply about that document: its thread.
-- +goose Up
CREATE TABLE link_messages (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),       -- whose copy
  link_id      uuid NOT NULL REFERENCES business_links(id),
  pair_id      uuid NOT NULL,                              -- the message, the same on both copies
  mine         boolean NOT NULL,                           -- this side sent it (a card: this side acted)
  kind         text NOT NULL CHECK (kind IN ('text', 'card')),
  body         text NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
  -- Who wrote it: a member's first name, shown under the business's name.
  sender_name  text NOT NULL DEFAULT '' CHECK (length(sender_name) <= 80),
  -- This side's member who sent it (on its own copy only).
  sender_id    text CHECK (length(sender_id) <= 200),
  -- A card: {doc: order|bill|invoice|purchase, event, reference, id
  -- (this side's own document), amount_kobo, note}.
  card         jsonb CHECK (card IS NULL OR jsonb_typeof(card) = 'object'),
  -- A photo, a PDF or a voice note sent with it, in this side's own
  -- storage (each business keeps its own copy): {kind: photo|file|voice,
  -- path, type, size, name}.
  attachment   jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object'),
  -- The message it quotes (its pair_id): a reply, and to a card, a reply
  -- about that card's document.
  reply_to     uuid,
  -- Forwarded from another conversation (WhatsApp's "Forwarded").
  forwarded    boolean NOT NULL DEFAULT false,
  -- The first link's preview, fetched by the sender's side when sent:
  -- {url, host, title, description} (no image; nothing more of the page).
  preview      jsonb CHECK (preview IS NULL OR jsonb_typeof(preview) = 'object'),
  -- Deleted for me: hidden from this side's view, never from the other's
  -- (each side keeps the record of the trade; a card is never hidden).
  hidden_at    timestamptz,
  -- The read mark: on the other side's message, when this side read it;
  -- on our own, when the other side did (mirrored).
  read_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, pair_id),
  CHECK ((kind = 'card') = (card IS NOT NULL))
);
CREATE INDEX link_messages_stream ON link_messages (tenant_id, link_id, created_at DESC);
CREATE INDEX link_messages_card ON link_messages (tenant_id, (card ->> 'doc'), (card ->> 'reference')) WHERE kind = 'card';
ALTER TABLE link_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON link_messages TO taskiem_app;

-- Each side's own settings for a conversation: muted (its people are not
-- told on their phones), archived (off the list until unarchived).
CREATE TABLE link_conversations (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  muted       boolean NOT NULL DEFAULT false,
  archived    boolean NOT NULL DEFAULT false,
  -- Pinned to the top of the list (up to three, as WhatsApp's).
  pinned_at   timestamptz,
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
ALTER TABLE link_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_conversations TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON link_conversations TO taskiem_app;

-- A message of the conversation told to a person's phone: the chat
-- app's id of what was sent (to whom, by the hash of the address, never
-- the address), so a reply quoting it there goes into the conversation,
-- quoting that message.
CREATE TABLE link_chat_sent (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  channel       text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack')),
  address_hash  bytea NOT NULL CHECK (length(address_hash) = 32),
  message_id    text NOT NULL CHECK (length(message_id) BETWEEN 1 AND 200),
  link_id       uuid NOT NULL REFERENCES business_links(id),
  pair_id       uuid NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (channel, address_hash, message_id)
);
ALTER TABLE link_chat_sent ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_chat_sent FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_chat_sent TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON link_chat_sent TO taskiem_app;

-- Who is typing in a conversation, for the other side's typing line: a
-- moment's state, no words, so it does not go through the door (whose
-- every entry is audited). No business reads the table: one definer
-- marks the caller's side typing for a few seconds, and another answers
-- whether the other side is.
CREATE TABLE link_typing (
  link_id    uuid NOT NULL REFERENCES business_links(id),
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  until      timestamptz NOT NULL,
  PRIMARY KEY (link_id, tenant_id)
);
ALTER TABLE link_typing ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_typing FORCE ROW LEVEL SECURITY;
CREATE POLICY no_app ON link_typing TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch_all ON link_typing TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON link_typing FROM PUBLIC;
GRANT SELECT (link_id, tenant_id, until), INSERT, UPDATE ON link_typing TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_link_typing(p_link uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
BEGIN
  IF NOT EXISTS (SELECT 1 FROM business_links WHERE id = p_link AND status = 'active' AND v_me IN (a_tenant, b_tenant)) THEN
    RETURN;
  END IF;
  INSERT INTO link_typing (link_id, tenant_id, until) VALUES (p_link, v_me, now() + interval '6 seconds')
    ON CONFLICT (link_id, tenant_id) DO UPDATE SET until = EXCLUDED.until;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_typing_of(p_link uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
BEGIN
  RETURN EXISTS (SELECT 1 FROM business_links l JOIN link_typing t ON t.link_id = l.id
    WHERE l.id = p_link AND l.status = 'active' AND v_me IN (l.a_tenant, l.b_tenant) AND t.tenant_id <> v_me AND t.until > now());
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_typing(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_link_typing_of(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_typing(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_link_typing_of(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_typing(uuid) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_link_typing_of(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_typing_of(uuid);
DROP FUNCTION taskiem_link_typing(uuid);
DROP TABLE link_typing;
DROP TABLE link_chat_sent;
DROP TABLE link_conversations;
DROP TABLE link_messages;
