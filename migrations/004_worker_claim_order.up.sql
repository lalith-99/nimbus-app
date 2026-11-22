-- One ordered range covers new jobs, retries, and expired processing leases.
-- UTC timestamp arithmetic keeps the five-minute lease independent of timezone.
CREATE FUNCTION nimbus_claimable_at(state text, created timestamptz, retry timestamptz, updated timestamptz)
RETURNS timestamp LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN state = 'processing' THEN (updated AT TIME ZONE 'UTC') + interval '5 minutes'
                WHEN state = 'pending' THEN coalesce(retry, created) AT TIME ZONE 'UTC'
           END
$$;

CREATE INDEX idx_notifications_claimable ON notifications
    (nimbus_claimable_at(status, created_at, next_retry_at, updated_at), created_at, id)
    WHERE status IN ('pending', 'processing');
