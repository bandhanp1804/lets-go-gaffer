DROP INDEX IF EXISTS alerts_user_idx;
ALTER TABLE alerts DROP COLUMN IF EXISTS user_id;
DROP INDEX IF EXISTS watchlist_player_idx;
DELETE FROM watchlist WHERE user_id <> '';
ALTER TABLE watchlist DROP CONSTRAINT watchlist_pkey;
ALTER TABLE watchlist ADD PRIMARY KEY (player_id);
ALTER TABLE watchlist DROP COLUMN user_id;
