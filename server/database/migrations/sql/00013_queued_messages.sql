-- +goose Up
--
-- queued_messages: prompts waiting for a Station's session to go idle (see
-- station.Queue). Held server-side rather than in each client so the queue
-- is shared by every app/TUI watching the station and survives any of them
-- closing; the server itself sends the whole queue as one prompt once the
-- running reply finishes. Ordered by id — a failed send re-inserts its
-- messages under their original ids, which puts them back at the front.
CREATE TABLE `queued_messages` (
    `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    `station_id` integer NOT NULL,
    `text` text NOT NULL,
    `created_at` datetime NOT NULL
);
CREATE INDEX `idx_queued_messages_station` ON `queued_messages`(`station_id`, `id`);

-- +goose Down
DROP TABLE IF EXISTS `queued_messages`;
