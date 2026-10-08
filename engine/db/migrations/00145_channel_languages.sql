-- Languages on WhatsApp, USSD and SMS (spec 11.6, decision 0029).
--
-- channel_languages is a number's language: chosen with the "language"
-- command, or detected from its first messages. Like whatsapp_contacts
-- (00035) it belongs to the number, not to a tenant: no tenant reads it,
-- and only the two functions below reach it. A chosen language is never
-- replaced by a detected one.
--
-- tenant_channel_settings is a tenant's choice: which draft languages its
-- people may get beyond those the operator turned on
-- (TASKIEM_LANGUAGES), the language USSD callers and new numbers get, and
-- whether voice notes are transcribed (off by default).

-- +goose Up
CREATE TABLE channel_languages (
  number      text PRIMARY KEY CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  language    text NOT NULL CHECK (language IN ('en', 'pcm', 'yo', 'ha', 'ig')),
  source      text NOT NULL CHECK (source IN ('command', 'detected')),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE channel_languages ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_languages FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON channel_languages TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON channel_languages TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON channel_languages FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON channel_languages TO taskiem_dispatch;

-- A number's language and how it was set; no row when none.
-- +goose StatementBegin
CREATE FUNCTION taskiem_lang_get(p_number text)
RETURNS TABLE (language text, source text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.language, l.source FROM channel_languages l WHERE l.number = p_number
$$;
-- +goose StatementEnd

-- Sets a number's language. A detected language never replaces a chosen
-- one. Rows untouched for a year are forgotten now and then.
-- +goose StatementBegin
CREATE FUNCTION taskiem_lang_set(p_number text, p_language text, p_source text)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF random() < 0.01 THEN
    DELETE FROM channel_languages l WHERE l.updated_at < now() - interval '1 year';
  END IF;
  INSERT INTO channel_languages AS l (number, language, source) VALUES (p_number, p_language, p_source)
  ON CONFLICT (number) DO UPDATE SET language = EXCLUDED.language, source = EXCLUDED.source, updated_at = now()
   WHERE l.source = 'detected' OR EXCLUDED.source = 'command';
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_lang_get(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_lang_set(text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_lang_get(text), taskiem_lang_set(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_lang_get(text), taskiem_lang_set(text, text, text) TO taskiem_app;

CREATE TABLE tenant_channel_settings (
  tenant_id         uuid PRIMARY KEY REFERENCES tenants(id),
  languages         text[] NOT NULL DEFAULT '{}' CHECK (languages <@ ARRAY['pcm', 'yo', 'ha', 'ig']::text[]),
  default_language  text NOT NULL DEFAULT 'en' CHECK (default_language IN ('en', 'pcm', 'yo', 'ha', 'ig')),
  voice_notes       boolean NOT NULL DEFAULT false,
  updated_by        text NOT NULL,
  updated_at        timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_channel_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_channel_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_channel_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_channel_settings TO taskiem_app;

-- +goose Down
DROP TABLE tenant_channel_settings;
DROP FUNCTION taskiem_lang_set(text, text, text);
DROP FUNCTION taskiem_lang_get(text);
DROP TABLE channel_languages;
