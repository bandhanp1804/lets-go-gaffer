CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE teams (
    id         INT PRIMARY KEY,          -- FPL team id
    name       TEXT NOT NULL,
    short_name TEXT NOT NULL
);

CREATE TABLE players (
    id           INT PRIMARY KEY,        -- FPL element id
    first_name   TEXT NOT NULL,
    last_name    TEXT NOT NULL,
    web_name     TEXT NOT NULL,
    team_id      INT NOT NULL REFERENCES teams (id),
    position     SMALLINT NOT NULL CHECK (position BETWEEN 1 AND 4),
    now_cost     INT NOT NULL,           -- tenths of millions, as in the FPL API
    total_points INT NOT NULL,
    form         NUMERIC(4, 1) NOT NULL,
    minutes      INT NOT NULL,
    status       TEXT NOT NULL,
    chance_of_playing_next_round SMALLINT,  -- NULL = not assessed
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX players_team_idx ON players (team_id);

CREATE TABLE player_history (
    player_id    INT NOT NULL REFERENCES players (id),
    gameweek     INT NOT NULL,
    fixture      INT NOT NULL,
    total_points INT NOT NULL,
    minutes      INT NOT NULL,
    goals_scored INT NOT NULL,
    assists      INT NOT NULL,
    clean_sheets INT NOT NULL,
    yellow_cards INT NOT NULL,
    red_cards    INT NOT NULL,
    value        INT NOT NULL,           -- cost that week, tenths of millions
    -- A double gameweek has two fixtures, so fixture is part of the key.
    PRIMARY KEY (player_id, gameweek, fixture)
);

CREATE TABLE news_articles (
    id           BIGSERIAL PRIMARY KEY,
    url          TEXT NOT NULL UNIQUE,   -- idempotent re-ingest
    source       TEXT NOT NULL,
    title        TEXT NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX news_articles_published_idx ON news_articles (published_at DESC);

-- 1024 matches Voyage AI's current models (Anthropic's recommended embedding
-- provider). The dimension is tied to the model: changing it later means a new
-- migration and a re-embed, which is cheap while data is small.
CREATE TABLE news_chunks (
    id         BIGSERIAL PRIMARY KEY,
    article_id BIGINT NOT NULL REFERENCES news_articles (id) ON DELETE CASCADE,
    chunk_idx  INT NOT NULL,
    content    TEXT NOT NULL,
    embedding  vector(1024) NOT NULL,
    UNIQUE (article_id, chunk_idx)
);
CREATE INDEX news_chunks_embedding_idx
    ON news_chunks USING hnsw (embedding vector_cosine_ops);

-- Which players a chunk mentions, for player-filtered retrieval.
CREATE TABLE chunk_players (
    chunk_id  BIGINT NOT NULL REFERENCES news_chunks (id) ON DELETE CASCADE,
    player_id INT NOT NULL REFERENCES players (id),
    PRIMARY KEY (chunk_id, player_id)
);
CREATE INDEX chunk_players_player_idx ON chunk_players (player_id);
