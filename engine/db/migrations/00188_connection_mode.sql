-- Whether a connection holds a live or a test key (no-technical-steps N2),
-- read from the key's start when it is saved: pickers and storefront
-- payments say "test" so nobody takes practice payments for real ones.

-- +goose Up
ALTER TABLE connections ADD COLUMN mode text CHECK (mode IS NULL OR mode ~ '^[a-z][a-z0-9_]{0,30}$');

-- +goose Down
ALTER TABLE connections DROP COLUMN mode;
