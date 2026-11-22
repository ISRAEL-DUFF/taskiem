-- A label merge is one step under two merges at once (docs/spec/
-- what-a-business-buys.md §5.4; 00503). Taskiem is not in production:
-- nothing is carried over.
--
-- - taskiem_spend_label_merge locked the label merged into only when it
--   was merged already (FOR SHARE on the rows its WHERE matched), so a
--   label not yet merged was never locked: merging L into K and K into M
--   at once each saw the other undone and both went in, a chain L→K→M.
--   It now locks the label merged into whatever it is, and reads its
--   merged_into after the lock: a merge of it under way finishes first
--   and this one is refused, or this one holds it and that one waits.
--   Merging a label others are merged into is refused as before; the row
--   being changed is locked by the UPDATE before the trigger reads, and
--   each merge into it takes FOR SHARE on it, so none slips past.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_spend_label_merge() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  target_merged uuid;
BEGIN
  IF NEW.merged_into IS NULL OR (TG_OP = 'UPDATE' AND NEW.merged_into IS NOT DISTINCT FROM OLD.merged_into) THEN
    RETURN NEW;
  END IF;
  -- The label merged into, locked whatever it is: a merge of it at the
  -- same time waits for this one (and then sees it, and is refused), or
  -- this waits for that one and reads it merged.
  SELECT merged_into INTO target_merged FROM spend_labels
    WHERE tenant_id = NEW.tenant_id AND id = NEW.merged_into FOR SHARE;
  IF target_merged IS NOT NULL THEN
    RAISE EXCEPTION 'a label is merged into one that is not itself merged' USING ERRCODE = 'check_violation';
  END IF;
  -- This row is locked (by the UPDATE, or not yet seen by others when
  -- inserted), and a merge into it locks it FOR SHARE first: every label
  -- merged into it is seen here.
  IF EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = NEW.tenant_id AND merged_into = NEW.id) THEN
    RAISE EXCEPTION 'a label others are merged into is not merged itself: move them first' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_spend_label_merge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.merged_into IS NULL OR (TG_OP = 'UPDATE' AND NEW.merged_into IS NOT DISTINCT FROM OLD.merged_into) THEN
    RETURN NEW;
  END IF;
  -- The label merged into is locked, so a merge of it at the same time
  -- waits and then sees this one (and is refused), or is seen here.
  IF EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = NEW.tenant_id AND id = NEW.merged_into AND merged_into IS NOT NULL FOR SHARE) THEN
    RAISE EXCEPTION 'a label is merged into one that is not itself merged' USING ERRCODE = 'check_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = NEW.tenant_id AND merged_into = NEW.id) THEN
    RAISE EXCEPTION 'a label others are merged into is not merged itself: move them first' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
