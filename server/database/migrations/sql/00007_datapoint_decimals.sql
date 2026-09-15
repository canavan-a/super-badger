-- +goose Up
--
-- Owner-supplied decimal precision for displaying a data point's value (e.g.
-- 0 for a whole-number counter, 2 for a precise ratio) — shown wherever the
-- app renders the value (top-bar badges, the settings list, the history
-- chart). Defaults to 1, matching the fixed toFixed(1)/toFixed(2) formatting
-- the app used before this was configurable.

ALTER TABLE `data_point_settings` ADD COLUMN `decimals` integer NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE `data_point_settings` DROP COLUMN `decimals`;
