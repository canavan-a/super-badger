-- +goose Up
--
-- Owner-supplied display name for a data point (e.g. "gpu_temp_c" -> "GPU
-- Temp"), shown on the station header's top-bar badges and in threshold
-- notifications instead of the raw metric key. Empty until set — callers
-- fall back to the raw key when it's blank.

ALTER TABLE `data_point_settings` ADD COLUMN `label` text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE `data_point_settings` DROP COLUMN `label`;
