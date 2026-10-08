-- +goose Up
--
-- commands: what a metric source advertises on its commands endpoint (see
-- docs/command-spec.md). Commands are actions, not measurements — invoking
-- one performs work on the endpoint's machine (e.g. toggling models on/off,
-- which can take minutes) — and they belong to the source as a whole, not
-- to any station. Rewritten wholesale on every poll, so commands an
-- endpoint stops advertising disappear within a tick. group_name/active/
-- confirm/options are display hints; options is a JSON array of
-- {value,label,active} for a picker command.
CREATE TABLE `commands` (
    `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    `source_id` integer NOT NULL,
    `path` text NOT NULL,
    `label` text NOT NULL DEFAULT '',
    `url` text NOT NULL,
    `position` integer NOT NULL DEFAULT 0,
    `group_name` text NOT NULL DEFAULT '',
    `active` integer NOT NULL DEFAULT 0,
    `confirm` text NOT NULL DEFAULT '',
    `options` text,
    `updated_at` datetime NOT NULL,
    CONSTRAINT `idx_commands_source_path` UNIQUE (`source_id`, `path`)
);

-- +goose Down
DROP TABLE IF EXISTS `commands`;
