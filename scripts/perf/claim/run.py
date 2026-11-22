"""Measure complete PostgreSQL claim transactions against disposable schemas.

Requires Python 3.10+ and psycopg 3. Supply a local test database through libpq
(PGDATABASE, PGHOST, etc.). Writes and deletes only randomly named schemas.
"""
import argparse
import hashlib
import json
import math
import os
import platform
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from threading import Barrier

import psycopg
from psycopg import sql

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]

BATCH = 10
CLIENTS = 8
EXPECTED = {uuid.UUID(hashlib.md5(str(i).encode()).hexdigest())
            for i in range(1, 1_000_001) if i % 1000 < 4}


def percentile(samples, quantile):
    return round(sorted(samples)[math.ceil(len(samples) * quantile) - 1], 3)


def configure(conn, schema):
    conn.execute(sql.SQL("SET search_path TO {}").format(sql.Identifier(schema)))
    conn.execute("SET statement_timeout = '60s'")


def run_arm(arm):
    schema = "nimbus_bench_" + uuid.uuid4().hex
    before = arm == "baseline"
    path = HERE / "baseline.sql" if before else ROOT / "internal/db/sql/claim_pending.sql"
    # Placeholder names keep the original positional ordering from the application.
    query = path.read_text().replace("$1", "%(batch)s").replace("$2", "%(lease)s")
    params = {"batch": BATCH, "lease": 300}

    with psycopg.connect(autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
        try:
            configure(admin, schema)
            for name in ["001_create_notifications", "002_create_dead_letter_queue"]:
                admin.execute((ROOT / "migrations" / (name + ".up.sql")).read_text())
            started = time.monotonic()
            admin.execute("SET statement_timeout = '4min'")
            admin.execute((HERE / "seed.sql").read_text())
            index_build_ms = index_bytes = 0
            if not before:
                index_start = time.monotonic()
                admin.execute((ROOT / "migrations/004_worker_claim_order.up.sql").read_text())
                index_build_ms = round((time.monotonic() - index_start) * 1000)
                index_bytes = admin.execute("SELECT pg_relation_size('idx_notifications_claimable')").fetchone()[0]
            admin.execute("VACUUM ANALYZE notifications")
            for _ in range(3):
                with admin.transaction(force_rollback=True):
                    admin.execute(query, params).fetchall()
            with admin.transaction(force_rollback=True):
                plan = admin.execute("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + query, params).fetchone()[0][0]

            # Include the time to read the returned rows and commit each batch.
            start = []
            gate = Barrier(CLIENTS, action=lambda: start.append(time.monotonic()), timeout=30)

            def drain():
                ids, latency = [], []
                with psycopg.connect(autocommit=True) as conn:
                    configure(conn, schema)
                    gate.wait()
                    while True:
                        tick = time.monotonic()
                        rows = conn.execute(query, params).fetchall()
                        duration = (time.monotonic() - tick) * 1000
                        if not rows:
                            break
                        latency.append(duration)
                        ids.extend(row[0] for row in rows)
                return ids, latency

            with ThreadPoolExecutor(CLIENTS) as pool:
                results = list(pool.map(lambda _: drain(), range(CLIENTS)))
            elapsed = time.monotonic() - start[0]
            ids = [i for claimed, _ in results for i in claimed]
            latency = [ms for _, times in results for ms in times]
            if time.monotonic() - started >= 300:
                raise RuntimeError("trial exceeded the five-minute lease horizon")
            if len(ids) != len(EXPECTED) or set(ids) != EXPECTED:
                raise AssertionError("duplicate, missing, or ineligible claim")
            return {"variant": arm, "claimed": len(ids), "duplicate_or_missing": 0,
                    "batches": len(latency), "seconds": round(elapsed, 3),
                    "claims_per_second": round(len(ids) / elapsed, 1),
                    "batch_p50_ms": percentile(latency, .50),
                    "batch_p95_ms": percentile(latency, .95), "batch_p99_ms": percentile(latency, .99),
                    "added_index_bytes": index_bytes, "index_build_ms": index_build_ms,
                    "plan": plan}
        finally:
            admin.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema)))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    with psycopg.connect() as connection:
        host = connection.info.host
        if host not in ("127.0.0.1", "::1", "localhost") and not host.startswith("/"):
            parser.error("use a disposable local PostgreSQL instance")
        settings = dict(connection.execute("SELECT name, setting FROM pg_settings WHERE name IN "
                                           "('server_version','shared_buffers','fsync','synchronous_commit')"))
    report = {"rows": 1_000_000, "clients": CLIENTS, "batch_size": BATCH, "postgres": settings,
              "python": platform.python_version(), "psycopg": psycopg.__version__,
              "machine": platform.machine(), "logical_cpu_affinity": len(os.sched_getaffinity(0))
              if hasattr(os, "sched_getaffinity") else os.cpu_count(), "trials": []}
    for variant in ("baseline", "indexed", "indexed", "baseline"):
        trial = run_arm(variant)
        report["trials"].append(trial)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
        print({key: value for key, value in trial.items() if key != "plan"}, flush=True)
