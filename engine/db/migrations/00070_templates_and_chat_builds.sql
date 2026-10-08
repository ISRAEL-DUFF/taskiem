-- The SME template library and building over WhatsApp (spec 11.1, B4).
--
-- Templates are files in the binary (templates/), not rows: what a tenant
-- keeps is provenance. A draft instantiated from a template, or saved from
-- an AI build that started from one, records the template's id on its
-- version; an AI build records the template it started from and the
-- channel it was asked on (the web app, or a WhatsApp conversation, whose
-- build is driven by the chat session and saved only as a draft).

-- +goose Up
ALTER TABLE workflow_versions ADD COLUMN template_id text CHECK (template_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$');
ALTER TABLE ai_builds ADD COLUMN template_id text CHECK (template_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$');
ALTER TABLE ai_builds ADD COLUMN channel text NOT NULL DEFAULT 'web' CHECK (channel IN ('web', 'whatsapp'));

-- +goose Down
ALTER TABLE ai_builds DROP COLUMN channel;
ALTER TABLE ai_builds DROP COLUMN template_id;
ALTER TABLE workflow_versions DROP COLUMN template_id;
