-- +goose NO TRANSACTION
-- One customer across the app and WhatsApp (00468): one WhatsApp thread
-- per app thread (the link), WhatsApp threads found by the subject of a
-- person's phone in the business, and the app threads not yet looked at
-- for a pair. customer_conversations grows with every customer, so the
-- indexes are built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE UNIQUE INDEX CONCURRENTLY customer_conversations_linked ON customer_conversations (linked_to) WHERE linked_to IS NOT NULL;
CREATE INDEX CONCURRENTLY customer_conversations_whatsapp_subject ON customer_conversations (tenant_id, subject_id) WHERE line = 'whatsapp';
CREATE INDEX CONCURRENTLY customer_conversations_link_due ON customer_conversations (id) WHERE line = 'app' AND link_checked_at IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS customer_conversations_link_due;
DROP INDEX CONCURRENTLY IF EXISTS customer_conversations_whatsapp_subject;
DROP INDEX CONCURRENTLY IF EXISTS customer_conversations_linked;
