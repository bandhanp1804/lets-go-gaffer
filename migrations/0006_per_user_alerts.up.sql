-- Alerts and the watchlist become each user's own once anyone can sign in. The owners keep one
-- shared inbox and watchlist: alerts with no user_id, and watchlist rows with user_id ''. Rows
-- from before this migration are the owners'.
ALTER TABLE watchlist ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE watchlist DROP CONSTRAINT watchlist_pkey;
ALTER TABLE watchlist ADD PRIMARY KEY (user_id, player_id);
CREATE INDEX watchlist_player_idx ON watchlist (player_id);

ALTER TABLE alerts ADD COLUMN user_id TEXT;
CREATE INDEX alerts_user_idx ON alerts (user_id, created_at DESC);
