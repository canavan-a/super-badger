-- +goose Up
--
-- Append-only time series for station_data_points, so the app can graph
-- numeric data points over time (see server/api/history.go). Mirrors
-- ../stealth-operation's field_values table shape — narrow (station_id, key,
-- ts, value), no surrogate identity needed for lookups — but plain SQLite
-- instead of a TimescaleDB hypertable: downsampling happens at query time
-- (bucket width computed from the actual data span, same as stealth-operation
-- — see queryDataPointHistory), and old rows are pruned periodically by the
-- poller (see metrics.Manager) instead of a hypertable retention policy.
-- recorded_at is stored as a Unix timestamp (integer seconds) rather than
-- datetime text so bucket math (`recorded_at / bucket_seconds`) is cheap
-- integer division instead of strftime parsing on every row.

CREATE TABLE `station_data_point_history` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`station_id` integer NOT NULL,
	`key` text NOT NULL,
	`value` real NOT NULL,
	`recorded_at` integer NOT NULL
);

CREATE INDEX `idx_station_data_point_history_lookup` ON `station_data_point_history`(`station_id`, `key`, `recorded_at`);

-- +goose Down
DROP TABLE `station_data_point_history`;
