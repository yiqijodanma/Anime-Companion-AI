-- This rollback is intentionally lossy: old binaries cannot represent non-Haruhi spaces.
DELETE FROM messages WHERE conversation_id <> 'direct-haruhi';
DELETE FROM memory_summaries WHERE conversation_id <> 'direct-haruhi';

DROP INDEX IF EXISTS idx_sum_scope_date;
DROP INDEX IF EXISTS idx_sum_scope_date_unique;
DROP INDEX IF EXISTS idx_msg_scope_created;
DROP INDEX IF EXISTS idx_msg_scope_date_sequence;
DROP INDEX IF EXISTS idx_msg_scope_date_turn;

ALTER TABLE memory_summaries DROP COLUMN IF EXISTS conversation_id;
ALTER TABLE messages
    DROP COLUMN IF EXISTS sequence,
    DROP COLUMN IF EXISTS batch_id,
    DROP COLUMN IF EXISTS speaker_id,
    DROP COLUMN IF EXISTS speaker_kind,
    DROP COLUMN IF EXISTS conversation_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_msg_identity_date_turn
    ON messages (channel, external_id, message_date, turn_id);
CREATE INDEX IF NOT EXISTS idx_msg_identity_created
    ON messages (channel, external_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sum_identity_date_unique
    ON memory_summaries (channel, external_id, message_date);
CREATE INDEX IF NOT EXISTS idx_sum_identity_date
    ON memory_summaries (channel, external_id, message_date);
