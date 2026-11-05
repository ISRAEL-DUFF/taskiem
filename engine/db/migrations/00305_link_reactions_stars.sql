-- Business links, L3: reactions and stars in the conversation (WhatsApp's:
-- a tap on a message adds an emoji to it, and a star keeps it).
--
-- A reaction is one person's emoji on a message, and both businesses see it:
-- the reactor's side keeps it as its own (mine, with the member who gave it:
-- one reaction per member per message, changed or taken back), and, through
-- taskiem_link_enter in the same transaction, the other side keeps a copy
-- (not mine), the two sharing reaction_id. The other side's copy names the
-- reactor by first name only. Only the six emoji WhatsApp offers are
-- allowed. Neither side can touch the other's copy; a reaction to a
-- message is gone with no one's say but its reactor's.
--
-- A star is a member's own: who starred what is no one else's business, and
-- it is never copied to the other side.
-- +goose Up
CREATE TABLE link_message_reactions (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),          -- whose copy
  link_id       uuid NOT NULL REFERENCES business_links(id),
  pair_id       uuid NOT NULL,                                 -- the message, the same on both copies
  reaction_id   uuid NOT NULL,                                 -- the reaction, the same on both copies
  mine          boolean NOT NULL,                              -- a member of this side gave it
  reactor_id    text CHECK (length(reactor_id) <= 200),        -- this side's member (own copy only)
  reactor_name  text NOT NULL DEFAULT '' CHECK (length(reactor_name) <= 80),
  emoji         text NOT NULL CHECK (emoji IN ('👍', '❤️', '😂', '😮', '😢', '🙏')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, reaction_id),
  FOREIGN KEY (tenant_id, pair_id) REFERENCES link_messages (tenant_id, pair_id),
  CHECK ((mine) = (reactor_id IS NOT NULL))
);
-- One reaction per member per message.
CREATE UNIQUE INDEX link_message_reactions_one ON link_message_reactions (tenant_id, pair_id, reactor_id) WHERE mine;
CREATE INDEX link_message_reactions_message ON link_message_reactions (tenant_id, pair_id, created_at);
-- The day's count (a reaction counts against the plan's messages per day).
CREATE INDEX link_message_reactions_sent ON link_message_reactions (tenant_id, created_at) WHERE mine;
ALTER TABLE link_message_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_message_reactions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_message_reactions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON link_message_reactions TO taskiem_app;

CREATE TABLE link_message_stars (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  pair_id     uuid NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id, pair_id),
  FOREIGN KEY (tenant_id, pair_id) REFERENCES link_messages (tenant_id, pair_id)
);
CREATE INDEX link_message_stars_recent ON link_message_stars (tenant_id, user_id, created_at DESC);
ALTER TABLE link_message_stars ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_message_stars FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_message_stars TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON link_message_stars TO taskiem_app;

-- +goose Down
DROP TABLE link_message_stars;
DROP TABLE link_message_reactions;
