-- Each signed-in user's saved team and FPL login token. Both are encrypted by the app
-- (internal/secretbox) with a key the database never sees; token_expires is kept plain so the
-- Setup page can say when the token runs out without decrypting it.
CREATE TABLE user_settings (
    user_id       TEXT PRIMARY KEY, -- the GitHub username, lower case; "local" off the cluster
    team_ct       BYTEA,
    token_ct      BYTEA,
    token_expires TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
