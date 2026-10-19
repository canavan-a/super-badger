-- +goose Up
--
-- auto_mode: when on, a reply that fails because the conversation outgrew
-- the model's context window is recovered by the server itself — it
-- compacts the session and then sends auto_continue_prompt (see
-- station.Service.RunAutoMode). An empty prompt falls back to the server's
-- default.

ALTER TABLE `stations` ADD COLUMN `auto_mode` boolean NOT NULL DEFAULT false;
ALTER TABLE `stations` ADD COLUMN `auto_continue_prompt` text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE `stations` DROP COLUMN `auto_continue_prompt`;
ALTER TABLE `stations` DROP COLUMN `auto_mode`;
