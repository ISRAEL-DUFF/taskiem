-- Bitbucket Cloud as a Git host (spec 10.3). Its repo is workspace/repo_slug
-- and an empty api_url is https://api.bitbucket.org/2.0.

-- +goose Up
ALTER TABLE git_connections DROP CONSTRAINT git_connections_provider_check;
ALTER TABLE git_connections ADD CONSTRAINT git_connections_provider_check CHECK (provider IN ('github', 'gitlab', 'bitbucket'));

-- +goose Down
ALTER TABLE git_connections DROP CONSTRAINT git_connections_provider_check;
ALTER TABLE git_connections ADD CONSTRAINT git_connections_provider_check CHECK (provider IN ('github', 'gitlab'));
