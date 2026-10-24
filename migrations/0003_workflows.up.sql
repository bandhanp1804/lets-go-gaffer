-- What the background workflows (`gaffer worker`) record and say.

-- An alert is one message for the manager: a watched player's news, a gameweek briefing,
-- a model-health warning. read_at is set once it has been seen.
CREATE TABLE alerts (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT        NOT NULL,
    title      TEXT        NOT NULL,
    body       TEXT        NOT NULL DEFAULT '',
    url        TEXT        NOT NULL DEFAULT '',
    player_id  INTEGER REFERENCES players (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at    TIMESTAMPTZ
);
CREATE INDEX alerts_created_at_idx ON alerts (created_at DESC);

-- Players whose news and status changes become alerts.
CREATE TABLE watchlist (
    player_id INTEGER PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One nightly eval: how the assistant did on the fixed cases.
CREATE TABLE eval_runs (
    id        BIGSERIAL PRIMARY KEY,
    at        TIMESTAMPTZ      NOT NULL DEFAULT now(),
    model     TEXT             NOT NULL,
    pass_rate DOUBLE PRECISION NOT NULL,
    runs      INTEGER          NOT NULL,
    failing   JSONB            NOT NULL DEFAULT '[]',
    tokens    INTEGER          NOT NULL DEFAULT 0
);

-- One backtest of one scoring model, after the history it was run on.
CREATE TABLE backtest_runs (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ      NOT NULL DEFAULT now(),
    through_gw INTEGER          NOT NULL,
    model      TEXT             NOT NULL,
    spearman   DOUBLE PRECISION NOT NULL,
    mae        DOUBLE PRECISION NOT NULL,
    top20      DOUBLE PRECISION NOT NULL
);

-- Gameweeks whose briefing was sent, so a restarted workflow never sends it twice.
CREATE TABLE briefings (
    gameweek INTEGER PRIMARY KEY,
    sent_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
