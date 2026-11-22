# Webhook backlog drain

When the polling worker claimed ten ready jobs, it sent those ten and then waited for another five-second tick. A queue of 200 accepted notifications needed twenty separate ticks. [PR #17](https://github.com/lalith-99/nimbus-app/pull/17) changes the scheduling decision. After a full batch has recorded its outcomes, the worker immediately asks PostgreSQL for the next batch. After a short batch, an empty queue, or a persistence error it returns to the idle timer. Claims remain bounded at ten and delivery stays sequential.

## Matched before and after

One 4-vCPU GitHub-hosted Linux runner (AMD EPYC 9V74, 15 GiB RAM) ran the original poller (`848cdbc`) and the backlog-draining poller (`2e57dd1`), in that order, in separate fresh Compose stacks. Each stack ran three consecutive bursts of 200 distinct REST creates with 32 concurrent clients. Elapsed time runs from the start of submission to the last accepted webhook at the receiver and includes initial poll delay.

| Burst | Original elapsed, s | New elapsed, s | Original delivery p95, ms | New delivery p95, ms |
|---:|---:|---:|---:|---:|
| 1 | 99.45 | 4.73 | 94,344.4 | 4,608.6 |
| 2 | 99.49 | 3.99 | 94,383.3 | 3,868.0 |
| 3 | 99.57 | 4.39 | 94,472.2 | 4,266.8 |

The median elapsed time was **99.49 s before and 4.39 s after** (22.6× faster for this burst). The initial idle interval and arrival time relative to the next poll influence small bursts. The large run below uses only the new revision. Both measured revisions use the same pre-index claim query; the separate [million-row claim benchmark](worker-claims.md) measures the subsequent indexing change.

## 10,000-notification burst

| Measurement | Observed |
|---|---:|
| REST clients / synthetic tenants | 32 / 125 |
| Accepted / recorded sent with one attempt / received | 10,000 / 10,000 / 10,000 |
| Missing / duplicate / invalid receipts | 0 / 0 / 0 |
| REST acceptance window / accepted per second | 7.42 s / 1346.9 |
| REST response p50 / p95 / p99 | 19.9 / 44.3 / 112.4 ms |
| First submission to last receipt / observed deliveries per second | 23.80 s / 420.2 |
| Submission-to-webhook p50 / p95 / p99 | 11.79 / 16.02 / 16.39 s |

Every request carries a distinct idempotency key and a sequence number. The harness reconciles the ID from each REST response against the database row and delivery headers, checks the tenant and entire webhook body, counts repeats, and verifies that every stored row ended as `sent` after one attempt. The first database sample after the large burst found 9,380 pending and 620 sent. The harness waits for the queue to drain before reconciling the receiver. [Raw trial results](webhook-load.json) · [Measured job](https://github.com/lalith-99/nimbus-app/actions/runs/45033202900/job/137241120031)

## Conditions and reproduction

One Go gateway, PostgreSQL 15 with pgvector, Redis 7 with AOF persistence, and a Python HTTP receiver share one runner with the load generator. Requests cross a local Docker bridge. The synthetic tenants each send at most 80 notifications, within the normal 100/minute tenant rate limit. The 128-byte padding accompanies the run ID and sequence number in every webhook. The receiver returns HTTP 200. The workload exercises the PostgreSQL polling path with SQS disabled. The measurements cover a burst followed by a drain. Longer steady traffic, slower remote providers, multiple gateways, and failure injection would change the result.

The [main CI workflow](../../.github/workflows/ci.yml) reruns the 10,000-notification reconciliation on the combined implementation. Its pass condition checks real completions and ID integrity. Timing is reported for comparison across runs.

The [comparison workflow](../../.github/workflows/webhook-load.yml) can be run manually in GitHub Actions. It pins the reference revision, rebuilds both stacks and uploads each raw JSON line. To reproduce the candidate burst with the same script locally, use a fresh disposable checkout with ports 8080, 9090 and 9080 available:

```sh
docker compose -p nimbus-perf up --build --wait
python3 scripts/perf/webhook-load.py --count 10000 --clients 32 --variant local --output /tmp/nimbus-webhook.jsonl
docker compose -p nimbus-perf down -v
```

The final command removes volumes for the disposable `nimbus-perf` Compose project.
