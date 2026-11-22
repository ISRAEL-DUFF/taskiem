-- Someone removed from a business kept their staff profile's Payrolla
-- link and saved pay account: the employee stayed taken by them and could
-- not be unlinked. Removals now clear both (dropLeaverLinksTx); this
-- clears those left behind by earlier removals.
-- +goose Up
UPDATE staff_profiles sp SET payrolla_employee_id = NULL, payrolla_name = NULL, payrolla_linked_by = NULL, payrolla_linked_at = NULL,
    saved_id = NULL, saved_linked_by = NULL, saved_linked_at = NULL
  WHERE (sp.payrolla_employee_id IS NOT NULL OR sp.payrolla_linked_at IS NOT NULL OR sp.saved_id IS NOT NULL OR sp.saved_linked_at IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.tenant_id = sp.tenant_id AND m.user_id = sp.user_id);

-- +goose Down
SELECT 1;
