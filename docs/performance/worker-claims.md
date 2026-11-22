# Claiming a batch from a million-row notification queue

The original poller combined due work and expired leases with an OR predicate and sorted matching rows by creation time. On the seeded million-row table, the pre-change plan visited 144,000 active index entries and discarded 140,000 rows before sorting 4,000 candidates to claim ten. Measuring an empty table or ten freshly created rows would have missed that work.

The worker now looks up the earliest eligible rows through a partial B-tree. A fresh notification is eligible at creation, a retry at its scheduled time, and a processing row five minutes after its claim. Terminal rows stay out of the index. The atomic statement locks candidates using `SKIP LOCKED`, so concurrent clients can take distinct batches. This intentionally orders jobs by eligibility, then by original creation time and ID. A blocked old retry cannot jump ahead of work that has been ready longer. [Batch SQL](../../internal/db/sql/claim_pending.sql) · [Migration](../../migrations/004_worker_claim_order.up.sql) · [Eligibility tests](../../internal/db/claim_test.go)

## Workload

Each trial creates and analyzes a fresh schema containing one million synthetic webhook rows across 128 tenants. The same deterministic generator creates 856,000 terminal rows, 80,000 future retries, 60,000 active leases, 2,000 ready first attempts, 1,000 due retries and 1,000 expired leases. Each payload carries a 128-byte body. The data set models a small runnable subset in retained history. The benchmark measures its drain phase, with no new arrivals.

Eight concurrent clients issue the application's claim SQL over loopback TCP, at most ten rows per transaction. Each fetches the returned rows and commits. Setup, three rollback warmups and an instrumented plan are outside the timed interval. Batch latency measures nonempty calls. Drain time includes the last empty probe by each worker. A trial fails if the returned IDs differ from the exact 4,000 eligible IDs or any ID appears twice. Timing ends before the five-minute reclaim horizon. The fixture runs baseline/indexed/indexed/baseline to expose run-to-run variation. Provider requests, SQS, API create latency and physical delivery are outside this measurement.

## Local measurements

PostgreSQL 16.2 and the Python 3.12.14/psycopg 3.3.9 client ran on one Linux x86-64 host (Intel Xeon Platinum 8573C, nine logical CPUs available to the client). PostgreSQL used 256 MiB of shared buffers, fsync on and synchronous commit on. The shared host and filesystem contribute to run-to-run variation.

| Sequence | Query | Batch p50 | Batch p95 | Batch p99 | Drain seconds | Rows claimed/s |
|---|---|---:|---:|---:|---:|---:|
| 1 | baseline | 48.7 ms | 116.0 ms | 522.9 ms | 3.501 | 1,142 |
| 2 | indexed | 7.0 ms | 19.9 ms | 31.4 ms | 0.459 | 8,712 |
| 3 | indexed | 6.5 ms | 14.9 ms | 30.3 ms | 0.400 | 10,002 |
| 4 | baseline | 47.6 ms | 70.7 ms | 469.0 ms | 3.077 | 1,300 |

Across the two run pairs, throughput rose 7.6× and 7.7×. All four arms claimed exactly the expected IDs. The warm instrumented plans show the old bitmap scan and sort replaced by an ordered index scan. The complete trials and `EXPLAIN (ANALYZE, BUFFERS)` plans are in the [raw results](worker-claims-local.json).

The added index occupied 7,168,000 bytes (6.84 MiB) for 144,000 active rows. Building it after loading one million rows took 284–313 ms on this host. Every transition into, within or out of active work now maintains an additional index. The supplied migration uses an ordinary index build, which blocks writes while building. Schedule the migration before switching binaries. A larger live deployment may require a separately managed concurrent index build. Changing the SQL eligibility function in the future also requires rebuilding its expression index.

## Reproduce

From the repo root with Docker, Python 3.10+ and a free loopback port:

```bash
docker run --rm -d --name nimbus-claim-perf -p 127.0.0.1:55432:5432 \
  -e POSTGRES_PASSWORD=local -e POSTGRES_DB=nimbus_claim_bench postgres:16.2 \
  -c shared_buffers=256MB
python3 -m venv /tmp/nimbus-claim-tools
/tmp/nimbus-claim-tools/bin/pip install 'psycopg[binary]==3.3.9'
export PGHOST=127.0.0.1 PGPORT=55432 PGUSER=postgres PGPASSWORD=local PGDATABASE=nimbus_claim_bench
until docker exec nimbus-claim-perf pg_isready -U postgres -d nimbus_claim_bench; do sleep 1; done
/tmp/nimbus-claim-tools/bin/python scripts/perf/claim/run.py --output /tmp/nimbus-claims.json
docker stop nimbus-claim-perf
```

Each trial creates a random schema and removes it on completion. The runner needs room to store and index a million rows plus PostgreSQL WAL. Use a disposable local database. [Seed](../../scripts/perf/claim/seed.sql) · [Benchmark](../../scripts/perf/claim/run.py) · [Original poller SQL at 848cdbc](https://github.com/lalith-99/nimbus-app/blob/848cdbc6ed7584433b066aa86c4a6c79ac3f1235/internal/db/repository.go#L303)
