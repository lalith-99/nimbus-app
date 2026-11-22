UPDATE notifications
SET status = 'processing', updated_at = NOW()
WHERE id IN (
	SELECT id
	FROM notifications
	WHERE (status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW()))
	   OR (status = 'processing' AND updated_at < NOW() - ($2 * INTERVAL '1 second'))
	ORDER BY created_at ASC
	LIMIT $1
	FOR UPDATE SKIP LOCKED
)
RETURNING
	id, tenant_id, user_id, channel, payload,
	status, attempt, error_message, next_retry_at,
	created_at, updated_at
