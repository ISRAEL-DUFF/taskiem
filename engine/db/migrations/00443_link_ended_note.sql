-- Ending a link with documents still in flight (docs/spec/business-links.md).
-- Ending freezes the orders and bills mirrored between the two businesses
-- where they are, and nothing told either side which those were. The side
-- that ends the link now writes a line naming them into its own
-- conversation, and this function writes the same line into the other
-- side's, where the ended link's door no longer reaches. It works only for
-- the business that ended the link, in the transaction that ended it
-- (ended_at is that transaction's time), so it cannot be used to write to
-- a business at any other moment. Ending itself is unchanged.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_ended_note(p_link uuid, p_body text)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  l business_links%ROWTYPE;
  v_other uuid;
  v_id uuid := gen_random_uuid();
  v_name text;
BEGIN
  IF p_body IS NULL OR length(btrim(p_body)) NOT BETWEEN 1 AND 1000 THEN
    RAISE EXCEPTION 'the note is 1 to 1,000 characters' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO l FROM business_links WHERE id = p_link AND v_me IN (a_tenant, b_tenant)
     AND status = 'ended' AND ended_by = v_me AND ended_at = now();
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  v_other := CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END;
  SELECT name INTO v_name FROM tenants WHERE id = v_me;
  INSERT INTO link_messages (id, tenant_id, link_id, pair_id, mine, kind, body, sender_name)
    VALUES (v_id, v_other, p_link, v_id, false, 'diary', btrim(p_body), left(COALESCE(v_name, ''), 80));
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_ended_note(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_ended_note(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_ended_note(uuid, text) TO taskiem_app;
-- The function's one write: a line in the other side's conversation.
CREATE POLICY dispatch_note ON link_messages FOR INSERT TO taskiem_dispatch WITH CHECK (kind = 'diary' AND NOT mine);
GRANT INSERT ON link_messages TO taskiem_dispatch;

-- +goose Down
REVOKE INSERT ON link_messages FROM taskiem_dispatch;
DROP POLICY dispatch_note ON link_messages;
DROP FUNCTION taskiem_link_ended_note(uuid, text);
