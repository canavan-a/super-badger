-- +goose Up
--
-- Station alias: an owner-settable tag that metric sources can target
-- instead of (or in addition to) a Station's numeric ID or exact Name (see
-- server/metrics/metrics.go's poll()), so renaming a station or reusing a
-- source payload across stations doesn't require exact-name matching.
--
-- Station color: a small owner-chosen accent (from a fixed client-side
-- preset list) carried into push notifications so a station's alerts are
-- visually distinguishable.
--
-- data_point_settings gains: order_index (owner-controlled top-bar/list
-- ordering, replacing the previous alphabetical-by-key default), and
-- hidden/hidden_since_updated_at (a "temp delete" — hide a data point until
-- a fresh value arrives, see ListStationDataPoints).
--
-- (top_bar_actions was originally added here too, but this file had already
-- been applied in some environments by the time it was added — goose tracks
-- applied migrations by version number, not content, so appending to an
-- already-run file silently never executes the new line there. It's its own
-- migration now: see 00010_station_top_bar_actions.sql.)

ALTER TABLE `stations` ADD COLUMN `alias` text;
CREATE UNIQUE INDEX `idx_stations_alias` ON `stations`(`alias`) WHERE `alias` IS NOT NULL AND `alias` != '';
ALTER TABLE `stations` ADD COLUMN `color` text NOT NULL DEFAULT '#4C8BF5';

ALTER TABLE `data_point_settings` ADD COLUMN `order_index` integer NOT NULL DEFAULT 0;
ALTER TABLE `data_point_settings` ADD COLUMN `hidden` boolean NOT NULL DEFAULT false;
ALTER TABLE `data_point_settings` ADD COLUMN `hidden_since_updated_at` datetime;

-- +goose Down
ALTER TABLE `data_point_settings` DROP COLUMN `hidden_since_updated_at`;
ALTER TABLE `data_point_settings` DROP COLUMN `hidden`;
ALTER TABLE `data_point_settings` DROP COLUMN `order_index`;

DROP INDEX `idx_stations_alias`;
ALTER TABLE `stations` DROP COLUMN `color`;
ALTER TABLE `stations` DROP COLUMN `alias`;
