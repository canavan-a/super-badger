-- +goose Up
--
-- commands: the `commands` section a metric endpoint reports inside each
-- station's JSON object (see docs/command-spec.md). Commands are actions,
-- not measurements — invoking one performs work on the endpoint's machine
-- (e.g. toggling models on/off, which can take up to a minute) rather than
-- reporting a number. Rewritten wholesale on every poll, so commands an
-- endpoint stops advertising disappear within a tick. One row per
-- (station, path), mirroring station_data_points.
CREATE TABLE `commands` (
    `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    `station_id` integer NOT NULL,
    `source_id` integer,
    `path` text NOT NULL,
    `label` text NOT NULL DEFAULT '',
    `url` text NOT NULL,
    `updated_at` datetime NOT NULL,
    CONSTRAINT `idx_commands_station_path` UNIQUE (`station_id`, `path`)
);

-- +goose Down
DROP TABLE IF EXISTS `commands`;
