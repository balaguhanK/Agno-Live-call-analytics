CREATE TABLE IF NOT EXISTS users (
    email         TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'agent')),
    agent_name    TEXT UNIQUE,
    password_hash TEXT NOT NULL,
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_sign_in  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS calls (
    id                     TEXT PRIMARY KEY,
    agent                  TEXT NOT NULL DEFAULT '',
    initiation_timestamp   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    caller_phone_number    TEXT NOT NULL DEFAULT '',
    system_phone_number    TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL DEFAULT 'In Progress',
    caller_sentiment       TEXT NOT NULL DEFAULT 'Neutral',
    caller_sentiment_trend TEXT NOT NULL DEFAULT 'Flat',
    caller_avg_sentiment   DOUBLE PRECISION NOT NULL DEFAULT 0,
    agent_avg_sentiment    DOUBLE PRECISION NOT NULL DEFAULT 0,
    agent_sentiment_trend  TEXT NOT NULL DEFAULT 'Flat',
    duration               TEXT NOT NULL DEFAULT '00:00:00',
    categories             TEXT[] NOT NULL DEFAULT '{}',
    alert_count            INT NOT NULL DEFAULT 0,
    summary                TEXT,
    sentiment_history      JSONB NOT NULL DEFAULT '[]'
);

-- Added after the first release; post-call analysis of caller, query and agent
ALTER TABLE calls ADD COLUMN IF NOT EXISTS analysis JSONB;

CREATE INDEX IF NOT EXISTS calls_agent_idx ON calls (agent, initiation_timestamp DESC);

CREATE TABLE IF NOT EXISTS transcript_segments (
    call_id         TEXT NOT NULL REFERENCES calls (id) ON DELETE CASCADE,
    seq             INT NOT NULL,
    speaker         TEXT NOT NULL,
    time_start      TEXT NOT NULL,
    time_end        TEXT NOT NULL,
    text            TEXT NOT NULL,
    sentiment       TEXT,
    sentiment_score DOUBLE PRECISION,
    emoji           TEXT,
    pii             TEXT[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (call_id, seq)
);

-- Added for inline Agent Assist answers and live issue / action / outcome tags
ALTER TABLE transcript_segments ADD COLUMN IF NOT EXISTS meta JSONB;
