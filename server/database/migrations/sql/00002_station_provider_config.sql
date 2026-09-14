-- +goose Up
--
-- Stations now own their model connection directly (base URL + API key)
-- instead of referencing a provider preconfigured in opencode.json — the
-- server registers/re-registers a per-station opencode provider at
-- activation time (see opencode.Client.UpsertProvider). `provider_id` is
-- kept but is now server-derived ("station-<id>"), not user-entered.
-- `directory` is the cwd opencode runs the session's agent in.

ALTER TABLE `stations` ADD COLUMN `base_url` text NOT NULL DEFAULT '';
ALTER TABLE `stations` ADD COLUMN `api_key` text NOT NULL DEFAULT '';
ALTER TABLE `stations` ADD COLUMN `directory` text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE `stations` DROP COLUMN `base_url`;
ALTER TABLE `stations` DROP COLUMN `api_key`;
ALTER TABLE `stations` DROP COLUMN `directory`;
