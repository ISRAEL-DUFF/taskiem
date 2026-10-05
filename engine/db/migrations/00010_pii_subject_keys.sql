-- Per-subject keys wrap under a tenant KEK version (spec 4.9, 9.4).

-- +goose Up
ALTER TABLE subject_keys ADD COLUMN kek_version int;
ALTER TABLE subject_keys ADD COLUMN category text;
