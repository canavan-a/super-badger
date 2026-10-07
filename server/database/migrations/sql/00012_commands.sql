-- +goose Up
--
-- commands: the `commands` section a metric endpoint reports at the TOP
-- LEVEL of its JSON (see docs/command-spec.md). Commands are actions, not
-- measurements — invoking one performs work on the endpoint's machine
-- (e.g. toggling models on/off, which can take up to a minute) rather than
-- reporting a number. Rewritten wholesale on every poll, so commands an
-- endpoint stops advertising disappear within a tick. The whole list is
-- global to the endpoint (one list per metric source), not per station.
CREATE TABLE `commands` (
    `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    `source_id` integer NOT NULL,
    `path` text NOT NULL,
    `label` text NOT NULL DEFAULT '',
    `url` text NOT NULL,
    `updated_at` datetime NOT NULL,
    CONSTRAINT `idx_commands_source_path` UNIQUE (`source_id`, `path`)
);

-- +goose Down
DROP TABLE IF EXISTS `commands`;
