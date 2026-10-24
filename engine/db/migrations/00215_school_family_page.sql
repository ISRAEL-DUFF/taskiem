-- The family page (decision 0046 point 8; docs/spec/schools.md 10 and
-- 11.3, B6; docs/schools.md#the-family-page; docs/public-host.md): one
-- page per family at /s/<token> on the form host, reached from a link in
-- any message the school sends, with every child's fees, payments,
-- receipts and account numbers; I have paid claims; payment plan
-- requests; and links minted per message with the school's expiry.
--
-- school_family_links is one row per link minted: the token's hash (the
-- token itself is only ever in the message), when it expires, and a
-- sealed view of what the public role may not open from the record (the
-- children's names, the links of their invoice pages and receipts),
-- encrypted under a key derived from the token itself, so the database
-- alone shows nothing and only whoever holds the link reads it.
--
-- school_family_inbox is what the page's forms leave for the api role
-- (as storefront orders and form submissions are left): a claim, a plan
-- request or a request for a new link, sealed under the public host's
-- inbox key. The public role writes it through
-- taskiem_public_school_family_ask alone, rate-limited per family; the
-- api role takes it within seconds, records the claim or the request
-- under the family's sealing, or sends the new link, and nothing a parent
-- types ever counts as a payment.
--
-- school_payment_claims and school_plan_requests are the records the
-- bursar sees: a claim beside the unmatched money in To match, resolved
-- when a payment for the student is counted or dismissed with a reason;
-- a plan request with the parent's instalments and words, approved
-- (with the instalments as decided) or declined with a note.
-- +goose Up
ALTER TABLE school_settings ADD COLUMN family_link_days int NOT NULL DEFAULT 3 CHECK (family_link_days BETWEEN 1 AND 30);
GRANT SELECT (family_link_days, siblings_share_credit, issuing_channel) ON school_settings TO taskiem_dispatch;

-- A message to a family on its guardians' chat apps (api/whatsapp_school_family.go).
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily', 'school_approval', 'school_family'));

CREATE TABLE school_family_links (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  family_id   uuid NOT NULL REFERENCES school_families(id),
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  view        bytea NOT NULL CHECK (length(view) <= 65536),  -- sealed under the token's key: names and links
  purpose     text NOT NULL DEFAULT 'message' CHECK (length(purpose) <= 40),
  expires_at  timestamptz NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX school_family_links_family ON school_family_links (tenant_id, family_id, created_at DESC);
ALTER TABLE school_family_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_family_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_family_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_family_links TO taskiem_app;
CREATE POLICY dispatch ON school_family_links FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON school_family_links TO taskiem_dispatch;

-- A message sent to a family's guardian with a link: its text, sealed
-- under the guardian's subject, which the chat outbox opens to send it.
CREATE TABLE school_family_messages (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  family_id    uuid NOT NULL REFERENCES school_families(id),
  link_id      uuid REFERENCES school_family_links(id) ON DELETE SET NULL,
  guardian_id  uuid REFERENCES school_guardians(id) ON DELETE SET NULL,
  channel      text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'sms', 'none')),
  purpose      text NOT NULL CHECK (length(purpose) BETWEEN 1 AND 40),
  text         jsonb NOT NULL,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX school_family_messages_family ON school_family_messages (tenant_id, family_id, created_at DESC);
ALTER TABLE school_family_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_family_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_family_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_family_messages TO taskiem_app;
CREATE POLICY dispatch ON school_family_messages FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, family_id, purpose, created_at) ON school_family_messages TO taskiem_dispatch;

CREATE TABLE school_payment_claims (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  family_id          uuid NOT NULL REFERENCES school_families(id),
  student_id         uuid NOT NULL REFERENCES school_students(id),
  amount_kobo        bigint NOT NULL CHECK (amount_kobo > 0),
  paid_on            date NOT NULL,
  how                text NOT NULL CHECK (how IN ('transfer', 'cash', 'pos', 'other')),
  details            jsonb NOT NULL,          -- sealed: {reference, note}
  status             text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'matched', 'dismissed')),
  created_at         timestamptz NOT NULL DEFAULT now(),
  resolved_at        timestamptz,
  resolved_by        text,
  resolution_reason  text CHECK (length(resolution_reason) <= 300),
  resolved_part      uuid REFERENCES customer_invoice_payments(id),
  CHECK ((status = 'open') = (resolved_at IS NULL))
);
CREATE INDEX school_payment_claims_open ON school_payment_claims (tenant_id, created_at) WHERE status = 'open';
CREATE INDEX school_payment_claims_student ON school_payment_claims (tenant_id, student_id, created_at DESC);
CREATE INDEX school_payment_claims_family ON school_payment_claims (tenant_id, family_id, created_at DESC);
ALTER TABLE school_payment_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_payment_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_payment_claims TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_payment_claims TO taskiem_app;
GRANT UPDATE (status, resolved_at, resolved_by, resolution_reason, resolved_part) ON school_payment_claims TO taskiem_app;
CREATE POLICY dispatch ON school_payment_claims FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, family_id, student_id, amount_kobo, paid_on, how, status, created_at, resolved_at) ON school_payment_claims TO taskiem_dispatch;

CREATE TABLE school_plan_requests (
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  family_id            uuid NOT NULL REFERENCES school_families(id),
  student_id           uuid NOT NULL REFERENCES school_students(id),
  instalments          jsonb NOT NULL CHECK (jsonb_typeof(instalments) = 'array' AND jsonb_array_length(instalments) BETWEEN 1 AND 6),
  message              jsonb NOT NULL,        -- sealed: the parent's words
  status               text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'approved', 'declined')),
  created_at           timestamptz NOT NULL DEFAULT now(),
  decided_at           timestamptz,
  decided_by           text,
  decided_user         uuid REFERENCES users(id),
  decision_note        text CHECK (length(decision_note) <= 300),
  decided_instalments  jsonb CHECK (decided_instalments IS NULL OR jsonb_typeof(decided_instalments) = 'array'),
  CHECK ((status = 'requested') = (decided_at IS NULL)),
  CHECK (status <> 'approved' OR decided_instalments IS NOT NULL)
);
CREATE UNIQUE INDEX school_plan_requests_one_open ON school_plan_requests (student_id) WHERE status = 'requested';
CREATE INDEX school_plan_requests_open ON school_plan_requests (tenant_id, created_at) WHERE status = 'requested';
CREATE INDEX school_plan_requests_student ON school_plan_requests (tenant_id, student_id, created_at DESC);
ALTER TABLE school_plan_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_plan_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_plan_requests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_plan_requests TO taskiem_app;
GRANT UPDATE (status, decided_at, decided_by, decided_user, decision_note, decided_instalments) ON school_plan_requests TO taskiem_app;
CREATE POLICY dispatch ON school_plan_requests FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, family_id, student_id, instalments, status, created_at, decided_at, decision_note, decided_instalments) ON school_plan_requests TO taskiem_dispatch;

-- What the page's forms leave: platform data, written by the public role
-- through taskiem_public_school_family_ask only, taken by the api role.
CREATE TABLE school_family_inbox (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  family_id      uuid NOT NULL REFERENCES school_families(id),
  link_id        uuid NOT NULL REFERENCES school_family_links(id),
  kind           text NOT NULL CHECK (kind IN ('claim', 'plan', 'link')),
  idem           bytea NOT NULL UNIQUE CHECK (length(idem) = 32),
  payload        bytea NOT NULL CHECK (length(payload) <= 4000),
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz
);
CREATE INDEX school_family_inbox_open ON school_family_inbox (created_at);
ALTER TABLE school_family_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_family_inbox FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON school_family_inbox TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON school_family_inbox TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON school_family_inbox TO taskiem_dispatch;

-- What the public functions below read of the record, and no more.
GRANT SELECT (id, admission_number, class_id, status, family_id) ON school_students TO taskiem_dispatch;
GRANT SELECT (id, name, arm) ON school_classes TO taskiem_dispatch;
CREATE POLICY dispatch ON school_terms FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, session_id, name, starts_on, current) ON school_terms TO taskiem_dispatch;
CREATE POLICY dispatch ON school_sessions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, label) ON school_sessions TO taskiem_dispatch;
GRANT SELECT (term_id, student_id) ON school_term_invoices TO taskiem_dispatch;
CREATE POLICY dispatch ON school_student_accounts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, student_id, channel, status, account_id) ON school_student_accounts TO taskiem_dispatch;
CREATE POLICY dispatch ON customer_accounts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, customer_id, account_number, bank_name) ON customer_accounts TO taskiem_dispatch;
CREATE POLICY dispatch ON money_entry_customers FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, entry_id, customer_id) ON money_entry_customers TO taskiem_dispatch;

-- The family a link opens, expired or not: the page says "ask the school
-- for a new link" for an expired one, never "not found", and offers to
-- send one. Nothing for a token nobody minted.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, family_id uuid, link_id uuid, expires_at timestamptz, view bytea, siblings_share boolean, link_days int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.tenant_id, l.family_id, l.id, l.expires_at, l.view, st.siblings_share_credit, st.family_link_days
    FROM school_family_links l
    JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
    JOIN school_settings st ON st.tenant_id = l.tenant_id AND st.enabled
   WHERE l.token_hash = p_token_hash
$$;
-- +goose StatementEnd

-- The family's students, for a link that has not expired: no names (the
-- link's sealed view carries them), each one's credit, and their own
-- account number on the school's issuing connection.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family_students(p_token_hash bytea)
RETURNS TABLE (student_id uuid, admission_number text, class text, status text, credit_kobo bigint, account_number text, bank_name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH l AS (
    SELECT f.tenant_id, f.family_id FROM taskiem_public_school_family(p_token_hash) f WHERE f.expires_at > now()
  )
  SELECT s.id, s.admission_number, c.name || CASE WHEN c.arm = '' THEN '' ELSE ' ' || c.arm END, s.status,
         (SELECT COALESCE(sum(GREATEST(COALESCE(taskiem_money_entry_left(mc.entry_id), 0), 0)), 0)::bigint FROM money_entry_customers mc
           WHERE mc.tenant_id = l.tenant_id AND mc.customer_id = 'key:student:' || s.id::text),
         ca.account_number, ca.bank_name
    FROM l
    JOIN school_students s ON s.family_id = l.family_id
    JOIN school_classes c ON c.id = s.class_id
    LEFT JOIN school_settings st ON st.tenant_id = l.tenant_id
    LEFT JOIN school_student_accounts sa ON sa.tenant_id = l.tenant_id AND sa.student_id = s.id AND sa.status = 'issued' AND sa.channel = st.issuing_channel
    LEFT JOIN customer_accounts ca ON ca.id = sa.account_id
   ORDER BY s.status = 'active' DESC, s.admission_number
$$;
-- +goose StatementEnd

-- The family's term invoices: this term's, and past terms' still open;
-- their lines, totals and status, nothing about the customer.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family_invoices(p_token_hash bytea)
RETURNS TABLE (invoice_id uuid, student_id uuid, term_id uuid, term text, current boolean, number text, status text, lines jsonb, total_kobo bigint, paid_kobo bigint, due_on date)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH l AS (
    SELECT f.tenant_id, f.family_id FROM taskiem_public_school_family(p_token_hash) f WHERE f.expires_at > now()
  )
  SELECT i.id, ti.student_id, t.id, t.name || ' ' || ss.label, t.current, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.due_on
    FROM l
    JOIN school_students s ON s.family_id = l.family_id
    JOIN school_term_invoices ti ON ti.student_id = s.id
    JOIN customer_invoices i ON i.id = ti.invoice_id AND i.sent_at IS NOT NULL AND i.status <> 'draft'
    JOIN school_terms t ON t.id = ti.term_id
    JOIN school_sessions ss ON ss.id = t.session_id
   WHERE t.current OR i.status IN ('sent', 'part_paid', 'overdue')
   ORDER BY t.starts_on DESC, i.number
$$;
-- +goose StatementEnd

-- The family's receipts: number, amount, date, standing or voided.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family_receipts(p_token_hash bytea)
RETURNS TABLE (receipt_id uuid, student_id uuid, invoice_id uuid, number text, amount_kobo bigint, paid_at timestamptz, voided boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH l AS (
    SELECT f.tenant_id, f.family_id FROM taskiem_public_school_family(p_token_hash) f WHERE f.expires_at > now()
  )
  SELECT r.id, r.student_id, r.invoice_id, r.number, r.amount_kobo, r.paid_at, r.voided_at IS NOT NULL
    FROM l JOIN school_students s ON s.family_id = l.family_id JOIN school_receipts r ON r.student_id = s.id
   ORDER BY r.paid_at DESC, r.number DESC LIMIT 200
$$;
-- +goose StatementEnd

-- The family's claims and plan requests, so the page shows what was sent
-- and what the school decided: amounts, dates and states, none of the
-- parent's words (sealed for the school).
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family_requests(p_token_hash bytea)
RETURNS TABLE (kind text, id uuid, student_id uuid, amount_kobo bigint, paid_on date, how text, status text, instalments jsonb, decided_instalments jsonb,
               decision_note text, created_at timestamptz, decided_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH l AS (
    SELECT f.tenant_id, f.family_id FROM taskiem_public_school_family(p_token_hash) f WHERE f.expires_at > now()
  )
  SELECT 'claim', c.id, c.student_id, c.amount_kobo, c.paid_on, c.how, c.status, NULL::jsonb, NULL::jsonb, NULL::text, c.created_at, c.resolved_at
    FROM l JOIN school_payment_claims c ON c.tenant_id = l.tenant_id AND c.family_id = l.family_id
   WHERE c.status = 'open' OR c.created_at > now() - interval '90 days'
  UNION ALL
  SELECT 'plan', p.id, p.student_id, NULL, NULL, NULL, p.status, p.instalments, p.decided_instalments, p.decision_note, p.created_at, p.decided_at
    FROM l JOIN school_plan_requests p ON p.tenant_id = l.tenant_id AND p.family_id = l.family_id
   WHERE p.status = 'requested' OR p.created_at > now() - interval '90 days'
   ORDER BY 11 DESC
$$;
-- +goose StatementEnd

-- The page leaves a claim, a plan request or a request for a new link for
-- the api role. 'ok' once it is left (or was already, by its idem key);
-- 'expired' for a claim or plan on an expired link; 'pending' for a plan
-- while the student's last one is undecided; 'limited' past the family's
-- limits (claims: 5 an hour and 20 a day; plans: 3 an hour; links: 3 an
-- hour and 10 a day); NULL for a token nobody minted.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_family_ask(p_token_hash bytea, p_kind text, p_student uuid, p_idem bytea, p_payload bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l record;
  hour int;
  day int;
BEGIN
  IF p_kind NOT IN ('claim', 'plan', 'link') OR p_idem IS NULL OR length(p_idem) <> 32 OR p_payload IS NULL OR length(p_payload) > 4000 THEN
    RETURN NULL;
  END IF;
  SELECT f.* INTO l FROM taskiem_public_school_family(p_token_hash) f;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM school_family_inbox i WHERE i.idem = p_idem) THEN
    RETURN 'ok';
  END IF;
  IF p_kind <> 'link' AND l.expires_at <= now() THEN
    RETURN 'expired';
  END IF;
  IF p_kind <> 'link' AND (p_student IS NULL OR NOT EXISTS (SELECT 1 FROM school_students s WHERE s.id = p_student AND s.family_id = l.family_id)) THEN
    RETURN NULL;
  END IF;
  SELECT count(*) INTO hour FROM school_family_inbox i WHERE i.family_id = l.family_id AND i.kind = p_kind AND i.created_at > now() - interval '1 hour';
  SELECT count(*) INTO day FROM school_family_inbox i WHERE i.family_id = l.family_id AND i.kind = p_kind AND i.created_at > now() - interval '1 day';
  CASE p_kind
  WHEN 'claim' THEN
    hour := hour + (SELECT count(*) FROM school_payment_claims c WHERE c.family_id = l.family_id AND c.created_at > now() - interval '1 hour');
    day := day + (SELECT count(*) FROM school_payment_claims c WHERE c.family_id = l.family_id AND c.created_at > now() - interval '1 day');
    IF hour >= 5 OR day >= 20 THEN
      RETURN 'limited';
    END IF;
  WHEN 'plan' THEN
    IF EXISTS (SELECT 1 FROM school_plan_requests p WHERE p.student_id = p_student AND p.status = 'requested')
       OR EXISTS (SELECT 1 FROM school_family_inbox i WHERE i.family_id = l.family_id AND i.kind = 'plan' AND i.created_at > now() - interval '1 hour') THEN
      RETURN 'pending';
    END IF;
    hour := hour + (SELECT count(*) FROM school_plan_requests p WHERE p.family_id = l.family_id AND p.created_at > now() - interval '1 hour');
    IF hour >= 3 THEN
      RETURN 'limited';
    END IF;
  ELSE
    hour := hour + (SELECT count(*) FROM school_family_messages m WHERE m.family_id = l.family_id AND m.purpose = 'link' AND m.created_at > now() - interval '1 hour');
    day := day + (SELECT count(*) FROM school_family_messages m WHERE m.family_id = l.family_id AND m.purpose = 'link' AND m.created_at > now() - interval '1 day');
    IF hour >= 3 OR day >= 10 THEN
      RETURN 'limited';
    END IF;
  END CASE;
  INSERT INTO school_family_inbox (id, tenant_id, family_id, link_id, kind, idem, payload)
  VALUES (gen_random_uuid(), l.tenant_id, l.family_id, l.link_id, p_kind, p_idem, p_payload);
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- The api role's side of the inbox.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_family_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, family_id uuid, link_id uuid, kind text, payload bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE school_family_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM school_family_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.family_id, i.link_id, i.kind, i.payload, i.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_family_inbox_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM school_family_inbox WHERE id = p_id
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_public_school_family(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_school_family_students(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_school_family_invoices(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_school_family_receipts(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_school_family_requests(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_school_family_ask(bytea, text, uuid, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_school_family_inbox_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_school_family_inbox_done(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_school_family(bytea), taskiem_public_school_family_students(bytea), taskiem_public_school_family_invoices(bytea),
  taskiem_public_school_family_receipts(bytea), taskiem_public_school_family_requests(bytea), taskiem_public_school_family_ask(bytea, text, uuid, bytea, bytea),
  taskiem_school_family_inbox_claim(int), taskiem_school_family_inbox_done(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_school_family(bytea), taskiem_public_school_family_students(bytea), taskiem_public_school_family_invoices(bytea),
  taskiem_public_school_family_receipts(bytea), taskiem_public_school_family_requests(bytea), taskiem_public_school_family_ask(bytea, text, uuid, bytea, bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_school_family_inbox_claim(int), taskiem_school_family_inbox_done(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_school_family_inbox_done(uuid);
DROP FUNCTION taskiem_school_family_inbox_claim(int);
DROP FUNCTION taskiem_public_school_family_ask(bytea, text, uuid, bytea, bytea);
DROP FUNCTION taskiem_public_school_family_requests(bytea);
DROP FUNCTION taskiem_public_school_family_receipts(bytea);
DROP FUNCTION taskiem_public_school_family_invoices(bytea);
DROP FUNCTION taskiem_public_school_family_students(bytea);
DROP FUNCTION taskiem_public_school_family(bytea);
REVOKE SELECT ON money_entry_customers FROM taskiem_dispatch;
DROP POLICY dispatch ON money_entry_customers;
REVOKE SELECT ON customer_accounts FROM taskiem_dispatch;
DROP POLICY dispatch ON customer_accounts;
REVOKE SELECT ON school_student_accounts FROM taskiem_dispatch;
DROP POLICY dispatch ON school_student_accounts;
REVOKE SELECT (term_id, student_id) ON school_term_invoices FROM taskiem_dispatch;
REVOKE SELECT ON school_sessions FROM taskiem_dispatch;
DROP POLICY dispatch ON school_sessions;
REVOKE SELECT ON school_terms FROM taskiem_dispatch;
DROP POLICY dispatch ON school_terms;
REVOKE SELECT (id, name, arm) ON school_classes FROM taskiem_dispatch;
REVOKE SELECT (id, admission_number, class_id, status, family_id) ON school_students FROM taskiem_dispatch;
DROP TABLE school_family_inbox;
DROP TABLE school_plan_requests;
DROP TABLE school_payment_claims;
DROP TABLE school_family_messages;
DROP TABLE school_family_links;
DELETE FROM whatsapp_outbox WHERE kind = 'school_family';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily', 'school_approval'));
REVOKE SELECT (family_link_days, siblings_share_credit, issuing_channel) ON school_settings FROM taskiem_dispatch;
ALTER TABLE school_settings DROP COLUMN family_link_days;
