ALTER TABLE messages
    ALTER COLUMN open_id TYPE varchar(128),
    ADD COLUMN IF NOT EXISTS channel varchar(16) NOT NULL DEFAULT 'wechat',
    ADD COLUMN IF NOT EXISTS external_id varchar(128),
    ADD COLUMN IF NOT EXISTS turn_id varchar(64),
    ADD COLUMN IF NOT EXISTS message_date date,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz;

UPDATE messages
SET external_id = open_id
WHERE external_id IS NULL;

UPDATE messages
SET turn_id = id::text
WHERE turn_id IS NULL;

UPDATE messages
SET message_date = (created_at AT TIME ZONE 'Asia/Shanghai')::date
WHERE message_date IS NULL;

UPDATE messages
SET archived_at = created_at
WHERE archived_at IS NULL;

ALTER TABLE messages
    ALTER COLUMN external_id SET NOT NULL,
    ALTER COLUMN turn_id SET NOT NULL,
    ALTER COLUMN message_date SET NOT NULL,
    ALTER COLUMN archived_at SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_identity_date_turn
    ON messages (channel, external_id, message_date, turn_id);

CREATE INDEX IF NOT EXISTS idx_msg_identity_created
    ON messages (channel, external_id, created_at);

CREATE INDEX IF NOT EXISTS idx_msg_date_channel
    ON messages (message_date, channel);

ALTER TABLE memory_summaries
    ALTER COLUMN open_id TYPE varchar(128),
    ADD COLUMN IF NOT EXISTS channel varchar(16) NOT NULL DEFAULT 'wechat',
    ADD COLUMN IF NOT EXISTS external_id varchar(128),
    ADD COLUMN IF NOT EXISTS message_date date,
    ADD COLUMN IF NOT EXISTS archived_at timestamptz;

UPDATE memory_summaries
SET external_id = open_id
WHERE external_id IS NULL;

UPDATE memory_summaries
SET message_date = (summary_date AT TIME ZONE 'Asia/Shanghai')::date
WHERE message_date IS NULL;

UPDATE memory_summaries
SET archived_at = created_at
WHERE archived_at IS NULL;

ALTER TABLE memory_summaries
    ALTER COLUMN external_id SET NOT NULL,
    ALTER COLUMN message_date SET NOT NULL,
    ALTER COLUMN archived_at SET NOT NULL;

WITH ranked_summaries AS (
    SELECT
        id,
        row_number() OVER (
            PARTITION BY channel, external_id, message_date
            ORDER BY summary_date DESC, created_at DESC, id DESC
        ) AS rn
    FROM memory_summaries
)
DELETE FROM memory_summaries
USING ranked_summaries
WHERE memory_summaries.id = ranked_summaries.id
  AND ranked_summaries.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_sum_identity_date_unique
    ON memory_summaries (channel, external_id, message_date);

CREATE INDEX IF NOT EXISTS idx_sum_identity_date
    ON memory_summaries (channel, external_id, message_date);
