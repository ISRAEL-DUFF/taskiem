-- Fronts F1e (docs/spec/fronts.md §5.5): a submission keeps the customer's
-- answers to its action's own questions, sealed under the customer as
-- their details are, so erasing the customer erases them. The run gets
-- them as trigger.answers, apart from Taskiem's record of the submission.

-- +goose Up
ALTER TABLE storefront_orders ADD COLUMN answers jsonb;

-- +goose Down
ALTER TABLE storefront_orders DROP COLUMN answers;
