-- Rollback: remove dead letter queue
DROP TRIGGER IF EXISTS dlq_updated_at ON dead_letter_notifications;
DROP FUNCTION IF EXISTS update_dlq_updated_at();
DROP TABLE IF EXISTS dead_letter_notifications;

-- Restore original status constraint
ALTER TABLE notifications 
DROP CONSTRAINT IF EXISTS chk_status;

ALTER TABLE notifications 
ADD CONSTRAINT chk_status CHECK (status IN ('pending', 'processing', 'sent', 'failed'));
