-- FPL's official per-player note ("Hamstring injury - 75% chance of playing") and
-- when it was written. Empty/NULL when the player has no news.
ALTER TABLE players
    ADD COLUMN news       TEXT NOT NULL DEFAULT '',
    ADD COLUMN news_added TIMESTAMPTZ;
