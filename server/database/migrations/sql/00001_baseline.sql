-- +goose Up
--
-- Baseline schema, captured from the pre-goose gorm.AutoMigrate path (see
-- database.Station / database.StationMetric struct tags). Existing local dev
-- databases created before goose was introduced already have this schema and
-- are pre-seeded (goose_db_version version 1 = applied) by RunMigrations —
-- see database/migrations/migrations.go.

CREATE TABLE `stations` (
	`id` integer PRIMARY KEY AUTOINCREMENT,
	`name` text NOT NULL,
	`provider_id` text NOT NULL,
	`model_id` text NOT NULL,
	`agent` text NOT NULL,
	`opencode_session_id` text,
	`status` text NOT NULL DEFAULT "idle",
	`created_at` datetime,
	`updated_at` datetime
);

CREATE UNIQUE INDEX `idx_stations_name` ON `stations`(`name`);

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

-- +goose Down
DROP TABLE `station_metrics`;
DROP TABLE `stations`;
