-- +goose NO TRANSACTION
-- Erasing a subject runs 00387's trigger inside the erasing transaction:
-- it finds a business's contacts, riders and suppliers by the subject of
-- their sealed phone. Without these indexes each erasure read every one of
-- them. They grow with every customer and rider, so the indexes are built
-- CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY reach_contacts_phone_subject ON reach_contacts (tenant_id, (phone->>'subject'));
CREATE INDEX CONCURRENTLY riders_phone_subject ON riders (tenant_id, (phone->>'subject'));
CREATE INDEX CONCURRENTLY suppliers_phone_subject ON suppliers (tenant_id, (phone->>'subject'));

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS suppliers_phone_subject;
DROP INDEX CONCURRENTLY IF EXISTS riders_phone_subject;
DROP INDEX CONCURRENTLY IF EXISTS reach_contacts_phone_subject;
