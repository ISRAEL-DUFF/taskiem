-- +goose Up
-- Forms that sell L2 (docs/spec/forms-that-sell.md §5.4): the One product
-- kind of front is offered and drawn. A front may be of that kind; its
-- settings (the promise line, the chips, the stock line) are its options,
-- the column every front has had since fronts F1.
ALTER TABLE storefront_fronts DROP CONSTRAINT storefront_fronts_kind_check;
ALTER TABLE storefront_fronts ADD CONSTRAINT storefront_fronts_kind_check
  CHECK (kind IN ('shop', 'one-product', 'food', 'services', 'hire', 'spaces', 'tickets', 'classes', 'quotes', 'questions', 'callbacks', 'custom'));

-- +goose Down
-- A One product front goes back to an ordinary front of one list, with
-- its settings left out.
UPDATE storefront_fronts SET kind = 'custom', options = '{}' WHERE kind = 'one-product';
ALTER TABLE storefront_fronts DROP CONSTRAINT storefront_fronts_kind_check;
ALTER TABLE storefront_fronts ADD CONSTRAINT storefront_fronts_kind_check
  CHECK (kind IN ('shop', 'food', 'services', 'hire', 'spaces', 'tickets', 'classes', 'quotes', 'questions', 'callbacks', 'custom'));
