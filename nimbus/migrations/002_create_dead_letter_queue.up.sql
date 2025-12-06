-- Dead Letter Queue for failed notifications
CREATE TABLE IF NOT EXISTS dead_letter_notifications (
    -- Identity
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Reference to original notification
    original_notification_id UUID NOT NULL,
    
    -- Copy of original data (for retry)
    tenant_id UUID NOT NULL,
    user_id UUID NOT NULL,
    channel VARCHAR(20) NOT NULL,
    payload JSONB NOT NULL,
    
    -- Failure info
    attempts INT NOT NULL,
    last_error TEXT NOT NULL,
    
    -- DLQ state
    status VARCHAR(20) NOT NULL DEFAULT 'pending',  -- pending, retried, discarded
    retried_notification_id UUID,  -- If retried, points to new notification
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- When moved to DLQ
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    
    CONSTRAINT chk_dlq_channel CHECK (channel IN ('email', 'sms', 'webhook')),
    CONSTRAINT chk_dlq_status CHECK (status IN ('pending', 'retried', 'discarded'))
);

-- Index for listing DLQ by tenant
CREATE INDEX idx_dlq_tenant ON dead_letter_notifications(tenant_id, created_at DESC);

-- Index for pending items
CREATE INDEX idx_dlq_pending ON dead_letter_notifications(status, created_at)
WHERE status = 'pending';

-- Add new status to notifications table
ALTER TABLE notifications 
DROP CONSTRAINT IF EXISTS chk_status;

ALTER TABLE notifications 
ADD CONSTRAINT chk_status CHECK (status IN ('pending', 'processing', 'sent', 'failed', 'dead_lettered'));

-- Trigger to update updated_at
CREATE OR REPLACE FUNCTION update_dlq_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER dlq_updated_at
    BEFORE UPDATE ON dead_letter_notifications
    FOR EACH ROW
    EXECUTE FUNCTION update_dlq_updated_at();
