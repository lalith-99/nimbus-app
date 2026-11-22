-- One million deterministic IDs, 128 tenants and a 128-byte webhook body.
-- Every thousand rows includes four due jobs: fresh, overdue retry, and stale processing.
INSERT INTO notifications
    (id, tenant_id, user_id, channel, payload, created_at, status, attempt, next_retry_at, updated_at)
SELECT md5(i::text)::uuid, md5((i % 128)::text)::uuid, md5((i % 4096)::text)::uuid, 'webhook',
       jsonb_build_object('url', 'http://receiver.local/events', 'body', repeat('x', 128)),
       now() - (1000001-i)*interval '1 second',
       CASE WHEN i % 1000 < 3 THEN 'pending' WHEN i % 1000 = 3 THEN 'processing'
            WHEN i % 1000 < 84 THEN 'pending' WHEN i % 1000 < 144 THEN 'processing' ELSE 'sent' END,
       CASE WHEN i % 1000 < 2 THEN 0 ELSE 1 END,
       CASE WHEN i % 1000 = 2 THEN now() - interval '1 minute'
            WHEN i % 1000 BETWEEN 4 AND 83 THEN now() + interval '1 day' END,
       CASE WHEN i % 1000 = 3 THEN now() - interval '10 minutes' ELSE now() END
FROM generate_series(1, 1000000) AS series(i);
