-- +goose Up
--
-- Replaces the hardcoded-field station_metrics table with the Super Badger
-- Station Standard API model: an arbitrary set of key/value data points per
-- Station (see server/metrics/metrics.go), plus the config needed to poll
-- multiple external sources and to configure per-data-point top-bar display
-- and threshold notifications.

DROP TABLE `station_metrics`;

CREATE TABLE `metric_sources` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`name` text NOT NULL,
	`url` text NOT NULL,
	`api_key` text,
	`poll_interval_seconds` integer NOT NULL DEFAULT 30,
	`enabled` boolean NOT NULL DEFAULT true,
	`created_at` datetime,
	`updated_at` datetime
);

CREATE TABLE `station_data_points` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`station_id` integer NOT NULL,
	`source_id` integer,
	`key` text NOT NULL,
	`value` real NOT NULL,
	`raw_json` text,
	`updated_at` datetime NOT NULL
);

CREATE UNIQUE INDEX `idx_station_data_points_station_key` ON `station_data_points`(`station_id`, `key`);

CREATE TABLE `data_point_settings` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`station_id` integer NOT NULL,
	`key` text NOT NULL,
	`show_on_top_bar` boolean NOT NULL DEFAULT false,
	`threshold_enabled` boolean NOT NULL DEFAULT false,
	`threshold_value` real NOT NULL DEFAULT 0,
	`threshold_direction` text NOT NULL DEFAULT 'above',
	`last_notified_value` real
);

CREATE UNIQUE INDEX `idx_data_point_settings_station_key` ON `data_point_settings`(`station_id`, `key`);

-- +goose Down
DROP TABLE `data_point_settings`;
DROP TABLE `station_data_points`;
DROP TABLE `metric_sources`;

CREATE TABLE `station_metrics` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`station_id` integer NOT NULL,
	`gpu_temp_c` real,
	`gpu_util_pct` real,
	`tokens_per_sec` real,
	`raw_json` text,
	`polled_at` datetime NOT NULL
);

CREATE UNIQUE INDEX `idx_station_metrics_station_id` ON `station_metrics`(`station_id`);
