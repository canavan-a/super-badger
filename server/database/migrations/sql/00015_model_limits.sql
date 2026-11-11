-- +goose Up
--
-- model_limits: per provider/model overrides of the context window and output
-- cap opencode compacts against. The super-badger opencode plugin detects
-- both from the engine (llama.cpp / Strata GET /props) and polls
-- GET /internal/limits for these; a NULL column keeps the detected value.

CREATE TABLE `model_limits` (
  `provider_id` text NOT NULL,
  `model_id` text NOT NULL,
  `context` integer,
  `output` integer,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`provider_id`, `model_id`)
);

-- +goose Down
DROP TABLE `model_limits`;
