-- +goose Up
--
-- super-badger now drives opencode 2, which can't resume opencode 1 session
-- IDs. Clear them; each station starts a new session on its next prompt or
-- when its chat is opened.

UPDATE `stations` SET `opencode_session_id` = '', `status` = 'idle';

-- +goose Down
SELECT 1;
