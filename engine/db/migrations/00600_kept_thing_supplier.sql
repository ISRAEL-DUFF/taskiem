-- A row of Things we have changes its supplier (docs/spec/what-a-business-buys.md
-- §5.3; build-plan-gaps H2, g-thing-extra): PATCH /v1/things/{id} takes
-- supplier_id (one of the business's suppliers, the composite foreign key of
-- 00503 keeps it in the business) or clear_supplier, audited. Nothing else on
-- the row is newly changeable.
-- +goose Up
GRANT UPDATE (supplier_id) ON kept_things TO taskiem_app;

-- +goose Down
REVOKE UPDATE (supplier_id) ON kept_things FROM taskiem_app;
