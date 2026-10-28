-- How many times each signed-in user used a limited feature (Ask, news search, news refresh)
-- on each day (UTC), so every API pod enforces the same daily limits.
CREATE TABLE usage_counts (
    user_id TEXT    NOT NULL,
    day     DATE    NOT NULL,
    kind    TEXT    NOT NULL,
    n       INTEGER NOT NULL,
    PRIMARY KEY (user_id, day, kind)
);
