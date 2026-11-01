-- Quotes (docs/spec/fronts.md §6, F7): a request for a price, plain
-- questions with nothing in the basket. The owner prices it (chat's
-- `quote AK-7QX4KD 45000`, or the API), which puts the order in
-- price_changed with the quote as its new basket and total, and the
-- customer accepts or declines it on the status page as a changed price
-- is; accepting takes the deposit at once. Nothing new in the schema but
-- the kind.

-- +goose Up
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote'));
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote'));

-- +goose Down
DELETE FROM storefront_orders WHERE kind = 'quote';
DELETE FROM storefront_inbox WHERE kind = 'quote';
DELETE FROM triggers WHERE storefront_action = 'quote';
DELETE FROM storefront_actions WHERE kind = 'quote';
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class'));
