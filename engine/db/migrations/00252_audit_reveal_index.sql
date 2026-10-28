-- Revealing a run's personal data is audited once per person, run and ten
-- minutes (a page refreshing what it revealed must not flood the chain):
-- the check looks up the actor's last reveal of a target.

-- +goose Up
CREATE INDEX audit_log_reveals ON audit_log (tenant_id, target, actor_id, at) WHERE action = 'pii.reveal';

-- +goose Down
DROP INDEX audit_log_reveals;
