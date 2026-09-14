-- +goose Up
--
-- Reverts 00002: dynamically registering a custom opencode provider per
-- Station (base_url/api_key) turned out not to work — confirmed live that
-- opencode's config.update API never actually wires a runtime-registered
-- provider into prompt-time model resolution (ProviderModelNotFoundError
-- every time, regardless of directory scoping), and its global config file
-- is read-only here anyway (Nix/home-manager managed). Stations go back to
-- referencing a provider_id already declared in opencode's own config.
-- `directory` is kept — that part works fine and is independently useful.

ALTER TABLE `stations` DROP COLUMN `base_url`;
ALTER TABLE `stations` DROP COLUMN `api_key`;

-- +goose Down
ALTER TABLE `stations` ADD COLUMN `base_url` text NOT NULL DEFAULT '';
ALTER TABLE `stations` ADD COLUMN `api_key` text NOT NULL DEFAULT '';
