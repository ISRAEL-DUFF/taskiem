-- The public connector catalogue, part 2: submissions and review (Phase 4
-- P4-6, decision 0020). A verified publisher submits a signed package
-- (taskiem-connector-package/v1); the API runs the automated checks and
-- stores the version as checks_failed or in_review. A reviewer (a Taskiem
-- operator on the reviewer list, never a member of the publisher's
-- tenant: four eyes) approves or rejects it; the publisher publishes an
-- approved version; a published version can be revoked, by the publisher
-- or a reviewer, and never comes back.
--
--   in_review -> approved -> published -> revoked
--       |           |
--       |           +-> withdrawn
--       +-> rejected, withdrawn
--   checks_failed -> withdrawn
--
-- A version's content (manifest, module, digests, signature, checks) never
-- changes once stored, even for the superuser; neither does a row go away.

-- +goose Up
CREATE TABLE catalogue_versions (
  id               uuid PRIMARY KEY,
  publisher_tenant uuid NOT NULL REFERENCES tenants(id),
  publisher        text NOT NULL,       -- the slug when submitted
  connector_id     text NOT NULL CHECK (connector_id ~ '^p_[a-z][a-z0-9]{1,29}_[a-z][a-z0-9_]*$' AND length(connector_id) <= 63),
  version          text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  manifest         text NOT NULL,
  module           bytea NOT NULL,
  module_digest    bytea NOT NULL CHECK (length(module_digest) = 32),
  package_digest   bytea NOT NULL CHECK (length(package_digest) = 32),
  key_id           text NOT NULL,
  signature        bytea NOT NULL,
  licence          text NOT NULL,
  source_url       text,
  conformance      jsonb NOT NULL,
  attestation      jsonb NOT NULL,
  checks           jsonb NOT NULL,      -- the automated stage's report
  state            text NOT NULL CHECK (state IN ('checks_failed', 'in_review', 'approved', 'rejected', 'published', 'withdrawn', 'revoked')),
  submitted_by     text NOT NULL,
  submitted_at     timestamptz NOT NULL DEFAULT now(),
  reviewed_by      text,
  reviewed_at      timestamptz,
  review_note      text,
  published_at     timestamptz,
  revoked_by       text,
  revoked_at       timestamptz,
  revoke_reason    text
);
-- One live submission per version; a rejected, withdrawn or failed one
-- may be submitted again, a published or revoked one never.
CREATE UNIQUE INDEX catalogue_versions_live ON catalogue_versions (connector_id, version)
  WHERE state NOT IN ('checks_failed', 'rejected', 'withdrawn');
CREATE INDEX catalogue_versions_state ON catalogue_versions (state, submitted_at);
CREATE INDEX catalogue_versions_publisher ON catalogue_versions (publisher_tenant, submitted_at DESC);

ALTER TABLE catalogue_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalogue_versions FORCE ROW LEVEL SECURITY;
-- A publisher sees its own submissions; every other tenant reads the
-- catalogue only through the functions in 00107.
CREATE POLICY tenant_isolation ON catalogue_versions TO taskiem_app
  USING (publisher_tenant = ANY (taskiem_tenant_scope())) WITH CHECK (publisher_tenant = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON catalogue_versions TO taskiem_app;
GRANT UPDATE (state, published_at, revoked_by, revoked_at, revoke_reason) ON catalogue_versions TO taskiem_app;
CREATE POLICY dispatch ON catalogue_versions TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON catalogue_versions TO taskiem_dispatch;

-- Content is immutable; states move only forward, and the reviewer's
-- moves (approve, reject, a reviewer's revocation) only through the
-- definer functions below, which run as taskiem_dispatch.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_versions_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_slug text;
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'catalogue versions are never deleted' USING ERRCODE = '42501';
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.state NOT IN ('checks_failed', 'in_review') OR NEW.reviewed_by IS NOT NULL OR NEW.published_at IS NOT NULL OR NEW.revoked_at IS NOT NULL THEN
      RAISE EXCEPTION 'a submission starts in checks_failed or in_review' USING ERRCODE = '42501';
    END IF;
    SELECT slug INTO v_slug FROM connector_publishers WHERE tenant_id = NEW.publisher_tenant AND status = 'verified';
    IF v_slug IS NULL OR v_slug <> NEW.publisher OR NEW.connector_id NOT LIKE 'p\_' || v_slug || '\_%' THEN
      RAISE EXCEPTION 'connector % is not in a verified namespace of its publisher', NEW.connector_id USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF (NEW.id, NEW.publisher_tenant, NEW.publisher, NEW.connector_id, NEW.version, NEW.manifest, NEW.module, NEW.module_digest, NEW.package_digest,
      NEW.key_id, NEW.signature, NEW.licence, NEW.source_url, NEW.conformance, NEW.attestation, NEW.checks, NEW.submitted_by, NEW.submitted_at)
     IS DISTINCT FROM
     (OLD.id, OLD.publisher_tenant, OLD.publisher, OLD.connector_id, OLD.version, OLD.manifest, OLD.module, OLD.module_digest, OLD.package_digest,
      OLD.key_id, OLD.signature, OLD.licence, OLD.source_url, OLD.conformance, OLD.attestation, OLD.checks, OLD.submitted_by, OLD.submitted_at) THEN
    RAISE EXCEPTION 'a catalogue version never changes; submit a new version' USING ERRCODE = '42501';
  END IF;
  IF NEW.state = OLD.state THEN
    RETURN NEW;
  END IF;
  IF NOT ((OLD.state, NEW.state) IN (('in_review', 'approved'), ('in_review', 'rejected'), ('in_review', 'withdrawn'), ('checks_failed', 'withdrawn'),
                                      ('approved', 'published'), ('approved', 'withdrawn'), ('published', 'revoked'))) THEN
    RAISE EXCEPTION 'a catalogue version cannot go from % to %', OLD.state, NEW.state USING ERRCODE = '42501';
  END IF;
  IF NEW.state IN ('approved', 'rejected') AND current_user <> 'taskiem_dispatch' THEN
    RAISE EXCEPTION 'only a reviewer approves or rejects a version' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER catalogue_versions_guard BEFORE INSERT OR UPDATE OR DELETE ON catalogue_versions
  FOR EACH ROW EXECUTE FUNCTION taskiem_catalogue_versions_guard();

-- The pipeline's history: one row per step, for the publisher and the
-- reviewer.
CREATE TABLE catalogue_events (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  version_id       uuid NOT NULL REFERENCES catalogue_versions(id),
  publisher_tenant uuid NOT NULL REFERENCES tenants(id),
  event            text NOT NULL CHECK (event IN ('submitted', 'checks_passed', 'checks_failed', 'approved', 'rejected', 'published', 'withdrawn', 'revoked')),
  actor            text NOT NULL,
  note             text,
  detail           jsonb NOT NULL DEFAULT '{}',
  at               timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX catalogue_events_version ON catalogue_events (version_id, id);
ALTER TABLE catalogue_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalogue_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON catalogue_events TO taskiem_app
  USING (publisher_tenant = ANY (taskiem_tenant_scope())) WITH CHECK (publisher_tenant = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON catalogue_events TO taskiem_app;
CREATE POLICY dispatch ON catalogue_events TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON catalogue_events TO taskiem_dispatch;

-- Who may review: Taskiem operators, by email, kept by the operator CLI.
-- No tenant reads or writes it.
CREATE TABLE catalogue_reviewers (
  email      text PRIMARY KEY CHECK (email = lower(email) AND email LIKE '%@%'),
  added_by   text NOT NULL,
  added_at   timestamptz NOT NULL DEFAULT now(),
  removed_by text,
  removed_at timestamptz
);
ALTER TABLE catalogue_reviewers ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalogue_reviewers FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON catalogue_reviewers TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON catalogue_reviewers TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON catalogue_reviewers TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_set_reviewer(p_email text, p_active boolean, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_active THEN
    INSERT INTO catalogue_reviewers (email, added_by) VALUES (lower(p_email), p_by)
    ON CONFLICT (email) DO UPDATE SET added_by = p_by, added_at = now(), removed_by = NULL, removed_at = NULL;
  ELSE
    UPDATE catalogue_reviewers SET removed_by = p_by, removed_at = now() WHERE email = lower(p_email) AND removed_at IS NULL;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_reviewers()
RETURNS TABLE (email text, added_by text, added_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT email, added_by, added_at FROM catalogue_reviewers WHERE removed_at IS NULL ORDER BY email
$$;
-- +goose StatementEnd

-- The review queue and a submission's details, for reviewers (never the
-- module itself, which the CLI does not need to judge it).
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_queue(p_states text[])
RETURNS TABLE (id uuid, publisher text, connector_id text, version text, state text, licence text, submitted_by text, submitted_at timestamptz,
               passed boolean, reviewed_by text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT id, publisher, connector_id, version, state, licence, submitted_by, submitted_at, COALESCE((checks->>'passed')::boolean, false), reviewed_by
    FROM catalogue_versions WHERE state = ANY (p_states) ORDER BY submitted_at
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_submission(p_id uuid)
RETURNS TABLE (id uuid, publisher_tenant uuid, publisher text, connector_id text, version text, state text, manifest text,
               module_digest text, package_digest text, key_id text, licence text, source_url text, attestation jsonb, checks jsonb,
               submitted_by text, submitted_at timestamptz, reviewed_by text, reviewed_at timestamptz, review_note text,
               published_at timestamptz, revoked_by text, revoked_at timestamptz, revoke_reason text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT id, publisher_tenant, publisher, connector_id, version, state, manifest, encode(module_digest, 'hex'), encode(package_digest, 'hex'), key_id,
         licence, source_url, attestation, checks, submitted_by, submitted_at, reviewed_by, reviewed_at, review_note,
         published_at, revoked_by, revoked_at, revoke_reason
    FROM catalogue_versions WHERE id = p_id
$$;
-- +goose StatementEnd

-- A reviewer's decision. Four eyes: the reviewer must be on the list and
-- must not belong to the publisher's tenant (a member could review their
-- own colleague's work, or their own). Returns the publisher's tenant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_review(p_id uuid, p_reviewer text, p_approve boolean, p_note text, p_checklist jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v catalogue_versions%ROWTYPE;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM catalogue_reviewers r WHERE r.email = lower(p_reviewer) AND r.removed_at IS NULL) THEN
    RAISE EXCEPTION '% is not a catalogue reviewer', p_reviewer USING ERRCODE = '42501';
  END IF;
  SELECT * INTO v FROM catalogue_versions WHERE id = p_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no submission %', p_id USING ERRCODE = 'P0002';
  END IF;
  IF v.state <> 'in_review' THEN
    RAISE EXCEPTION 'submission % is %, not in review', p_id, v.state USING ERRCODE = '55000';
  END IF;
  IF lower(p_reviewer) = lower(v.submitted_by)
     OR EXISTS (SELECT 1 FROM users u WHERE u.id::text = v.submitted_by AND lower(u.email) = lower(p_reviewer))
     OR EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
                 WHERE m.tenant_id = v.publisher_tenant AND lower(u.email) = lower(p_reviewer)) THEN
    RAISE EXCEPTION '% belongs to the publisher: someone outside it must review (four eyes)', p_reviewer USING ERRCODE = '42501';
  END IF;
  IF COALESCE(trim(p_note), '') = '' THEN
    RAISE EXCEPTION 'a review needs a note' USING ERRCODE = '22023';
  END IF;
  UPDATE catalogue_versions SET state = CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END,
         reviewed_by = lower(p_reviewer), reviewed_at = now(), review_note = p_note
   WHERE id = p_id;
  INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note, detail)
  VALUES (p_id, v.publisher_tenant, CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END, 'reviewer:' || lower(p_reviewer), p_note,
          jsonb_build_object('checklist', COALESCE(p_checklist, '{}'::jsonb)));
  RETURN v.publisher_tenant;
END
$$;
-- +goose StatementEnd

-- A reviewer's revocation: the kill switch for a published version.
-- Returns the publisher's tenant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_revoke(p_connector text, p_version text, p_reviewer text, p_reason text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v catalogue_versions%ROWTYPE;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM catalogue_reviewers r WHERE r.email = lower(p_reviewer) AND r.removed_at IS NULL) THEN
    RAISE EXCEPTION '% is not a catalogue reviewer', p_reviewer USING ERRCODE = '42501';
  END IF;
  IF COALESCE(trim(p_reason), '') = '' THEN
    RAISE EXCEPTION 'a revocation needs a reason' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO v FROM catalogue_versions WHERE connector_id = p_connector AND version = p_version AND state = 'published' FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION '% % is not published', p_connector, p_version USING ERRCODE = 'P0002';
  END IF;
  UPDATE catalogue_versions SET state = 'revoked', revoked_by = 'reviewer:' || lower(p_reviewer), revoked_at = now(), revoke_reason = p_reason WHERE id = v.id;
  INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note)
  VALUES (v.id, v.publisher_tenant, 'revoked', 'reviewer:' || lower(p_reviewer), p_reason);
  RETURN v.publisher_tenant;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_catalogue_set_reviewer(text, boolean, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_reviewers() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_queue(text[]) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_submission(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_review(uuid, text, boolean, text, jsonb) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_revoke(text, text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_catalogue_set_reviewer(text, boolean, text), taskiem_catalogue_reviewers(), taskiem_catalogue_queue(text[]),
  taskiem_catalogue_submission(uuid), taskiem_catalogue_review(uuid, text, boolean, text, jsonb), taskiem_catalogue_revoke(text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_catalogue_set_reviewer(text, boolean, text), taskiem_catalogue_reviewers(), taskiem_catalogue_queue(text[]),
  taskiem_catalogue_submission(uuid), taskiem_catalogue_review(uuid, text, boolean, text, jsonb), taskiem_catalogue_revoke(text, text, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_catalogue_revoke(text, text, text, text);
DROP FUNCTION taskiem_catalogue_review(uuid, text, boolean, text, jsonb);
DROP FUNCTION taskiem_catalogue_submission(uuid);
DROP FUNCTION taskiem_catalogue_queue(text[]);
DROP FUNCTION taskiem_catalogue_reviewers();
DROP FUNCTION taskiem_catalogue_set_reviewer(text, boolean, text);
DROP TABLE catalogue_reviewers;
DROP TABLE catalogue_events;
DROP TABLE catalogue_versions;
DROP FUNCTION taskiem_catalogue_versions_guard();
