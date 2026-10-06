-- +goose Up
--
-- hidden: the station stays fully configured (session, data points,
-- notifications) but is left off the app's home list, drawer, and swipe
-- rotation — toggled from the app's Settings → Stations tab.
-- position: owner-chosen display/rotation order, set by drag and drop in
-- that same tab (see server/api's updateStationLayout). Backfilled from id so
-- existing installs keep the order the app's swipe rotation already used.

ALTER TABLE `stations` ADD COLUMN `hidden` boolean NOT NULL DEFAULT false;
ALTER TABLE `stations` ADD COLUMN `position` integer NOT NULL DEFAULT 0;
UPDATE `stations` SET `position` = `id`;

-- +goose Down
ALTER TABLE `stations` DROP COLUMN `position`;
ALTER TABLE `stations` DROP COLUMN `hidden`;
