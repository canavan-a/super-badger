-- +goose Up
--
-- top_bar_actions: which of the fixed header action buttons (compact,
-- reset, delete) and badges (tokens) the owner has opted into showing on
-- the top bar — a JSON array of keys, e.g. `["compact","reset","tokens"]`.
-- NULL/empty means none, matching every other top-bar element's "nothing by
-- default, opt in" rule (see server/api's stationOut/updateStation).

ALTER TABLE `stations` ADD COLUMN `top_bar_actions` text;

-- +goose Down
ALTER TABLE `stations` DROP COLUMN `top_bar_actions`;
