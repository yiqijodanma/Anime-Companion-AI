ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS conversation_id varchar(64) NOT NULL DEFAULT 'direct-haruhi',
    ADD COLUMN IF NOT EXISTS speaker_kind varchar(16) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS speaker_id varchar(32) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS batch_id varchar(96) NOT NULL DEFAULT 'legacy-unassigned',
    ADD COLUMN IF NOT EXISTS sequence bigint NOT NULL DEFAULT 0;

UPDATE messages
SET speaker_kind = CASE WHEN role = 'assistant' THEN 'character' ELSE 'user' END,
    speaker_id = CASE WHEN role = 'assistant' THEN 'haruhi' ELSE 'user' END;

WITH ordered AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY channel, external_id, conversation_id, message_date
               ORDER BY created_at, id
           ) AS seq,
           sum(CASE WHEN role = 'user' THEN 1 ELSE 0 END) OVER (
               PARTITION BY channel, external_id, conversation_id, message_date
               ORDER BY created_at, id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
           ) AS user_group
    FROM messages
), backfill AS (
    SELECT id, seq,
           CASE WHEN user_group = 0
                THEN 'legacy-orphan-' || id::text
                ELSE 'legacy-' || user_group::text
           END AS legacy_batch
    FROM ordered
)
UPDATE messages AS message
SET sequence = backfill.seq,
    batch_id = backfill.legacy_batch
FROM backfill
WHERE message.id = backfill.id;

DROP INDEX IF EXISTS idx_msg_identity_date_turn;
DROP INDEX IF EXISTS idx_msg_identity_created;

CREATE UNIQUE INDEX idx_msg_scope_date_turn
    ON messages (channel, external_id, conversation_id, message_date, turn_id);
CREATE UNIQUE INDEX idx_msg_scope_date_sequence
    ON messages (channel, external_id, conversation_id, message_date, sequence)
    WHERE sequence > 0;
CREATE INDEX idx_msg_scope_created
    ON messages (channel, external_id, conversation_id, created_at, id);

ALTER TABLE memory_summaries
    ADD COLUMN IF NOT EXISTS conversation_id varchar(64) NOT NULL DEFAULT 'direct-haruhi';

DROP INDEX IF EXISTS idx_sum_identity_date_unique;
DROP INDEX IF EXISTS idx_sum_identity_date;

CREATE UNIQUE INDEX idx_sum_scope_date_unique
    ON memory_summaries (channel, external_id, conversation_id, message_date);
CREATE INDEX idx_sum_scope_date
    ON memory_summaries (channel, external_id, conversation_id, message_date);
