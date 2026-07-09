CREATE TABLE IF NOT EXISTS messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    open_id varchar(128) NOT NULL,
    role varchar(16) NOT NULL,
    content text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_msg_openid_created
    ON messages (open_id, created_at);

CREATE TABLE IF NOT EXISTS memory_summaries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    open_id varchar(128) NOT NULL,
    summary_date timestamptz NOT NULL,
    content text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sum_openid_date
    ON memory_summaries (open_id, summary_date);
