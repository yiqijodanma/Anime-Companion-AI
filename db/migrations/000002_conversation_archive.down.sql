DROP INDEX IF EXISTS idx_sum_identity_date;
DROP INDEX IF EXISTS idx_sum_identity_date_unique;
DROP INDEX IF EXISTS idx_msg_date_channel;
DROP INDEX IF EXISTS idx_msg_identity_created;
DROP INDEX IF EXISTS idx_msg_identity_date_turn;

ALTER TABLE memory_summaries
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS message_date,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS channel;

ALTER TABLE messages
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS message_date,
    DROP COLUMN IF EXISTS turn_id,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS channel;
