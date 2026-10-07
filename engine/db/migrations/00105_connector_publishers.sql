-- The public connector catalogue, part 1: publisher namespaces (Phase 4
-- P4-6, decision 0020; docs/connector-submissions.md). A tenant that wants
-- to publish connectors to every tenant asks for a namespace (slug): its
-- catalogue connectors are p_<slug>_<name>, so ids can be neither squatted
-- nor confused with built-ins or tenants' own x_ connectors. A namespace
-- is usable only once a Taskiem operator has verified the publisher
-- (taskiem catalogue publishers verify). Packages are signed with the
-- publisher's Ed25519 key, whose public half is registered here.

-- +goose Up
CREATE TABLE connector_publishers (
  tenant_id      uuid PRIMARY KEY REFERENCES tenants(id),
  slug           text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][a-z0-9]{1,29}$'),
  name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  public_key     bytea NOT NULL CHECK (length(public_key) = 32),
  key_id         text NOT NULL,
  status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'suspended')),
  requested_by   text NOT NULL,
  requested_at   timestamptz NOT NULL DEFAULT now(),
  key_rotated_at timestamptz,
  verified_by    text,
  verified_at    timestamptz,
  status_note    text
);
ALTER TABLE connector_publishers ENABLE ROW LEVEL SECURITY;
ALTER TABLE connector_publishers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connector_publishers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- A tenant asks for a namespace and rotates its key; it never sets its
-- own status: only the operator's function below does.
GRANT SELECT ON connector_publishers TO taskiem_app;
GRANT INSERT (tenant_id, slug, name, public_key, key_id, requested_by) ON connector_publishers TO taskiem_app;
GRANT UPDATE (name, public_key, key_id, key_rotated_at) ON connector_publishers TO taskiem_app;
-- The catalogue's functions (owned by taskiem_dispatch) read a publisher's
-- public identity, and the operator's sets its status.
CREATE POLICY dispatch ON connector_publishers TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (tenant_id, slug, name, public_key, key_id, status, requested_at, verified_by, verified_at, status_note) ON connector_publishers TO taskiem_dispatch;
GRANT UPDATE (status, verified_by, verified_at, status_note) ON connector_publishers TO taskiem_dispatch;

-- The operator verifies, suspends or reinstates a publisher
-- (taskiem catalogue publishers). Returns the publisher's tenant, for the
-- audit entry the CLI writes in its chain.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_set_publisher(p_slug text, p_status text, p_by text, p_note text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_tenant uuid;
BEGIN
  IF p_status NOT IN ('verified', 'suspended', 'pending') THEN
    RAISE EXCEPTION 'unknown publisher status %', p_status USING ERRCODE = '22023';
  END IF;
  UPDATE connector_publishers SET status = p_status, status_note = NULLIF(p_note, ''),
         verified_by = CASE WHEN p_status = 'verified' THEN p_by ELSE verified_by END,
         verified_at = CASE WHEN p_status = 'verified' THEN now() ELSE verified_at END
   WHERE slug = p_slug RETURNING tenant_id INTO v_tenant;
  IF v_tenant IS NULL THEN
    RAISE EXCEPTION 'no publisher %', p_slug USING ERRCODE = 'P0002';
  END IF;
  RETURN v_tenant;
END
$$;
-- +goose StatementEnd

-- Every publisher, for the operator.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_publishers()
RETURNS TABLE (tenant_id uuid, slug text, name text, key_id text, status text, requested_at timestamptz, verified_by text, verified_at timestamptz, status_note text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id, slug, name, key_id, status, requested_at, verified_by, verified_at, status_note FROM connector_publishers ORDER BY slug
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_catalogue_set_publisher(text, text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_publishers() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_catalogue_set_publisher(text, text, text, text), taskiem_catalogue_publishers() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_catalogue_set_publisher(text, text, text, text), taskiem_catalogue_publishers() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_catalogue_publishers();
DROP FUNCTION taskiem_catalogue_set_publisher(text, text, text, text);
DROP TABLE connector_publishers;
