-- Erasing a person who came back (decision 0062 P2): a subject's later
-- generations are <subject>~2, ~3..., found by the range
--   subject_id ~>=~ (<base> || '~') AND subject_id ~<~ (<base> || chr(127))
-- (every id that starts with "<base>~", and nothing else: ids are ASCII,
-- categories lower-case letters and underscores). The byte-wise operators
-- are served by text_pattern_ops indexes whatever the database collation,
-- with the base as a parameter (a generic plan too), where the left(...)
-- comparison they replace read every key of the tenant.
--
-- signals_correlation lets erasure delete the signals buffered for a
-- person (a chat message's correlation is its sender) without reading the
-- tenant's whole buffer.

-- +goose Up
CREATE INDEX subject_keys_generations ON subject_keys (tenant_id, subject_id text_pattern_ops);
CREATE INDEX runs_customer_generations ON runs (tenant_id, customer_subject text_pattern_ops) WHERE customer_subject IS NOT NULL;
CREATE INDEX assistant_contacts_generations ON assistant_contacts (tenant_id, subject text_pattern_ops);
CREATE INDEX signals_correlation ON signals (tenant_id, correlation);

-- +goose Down
DROP INDEX signals_correlation;
DROP INDEX assistant_contacts_generations;
DROP INDEX runs_customer_generations;
DROP INDEX subject_keys_generations;
