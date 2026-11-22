#!/usr/bin/env python3
"""Burst through a disposable Compose gateway. Reconcile REST, PostgreSQL and the receiver."""
import argparse
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
import json
import math
import subprocess
import time
import uuid
from pathlib import Path
from urllib.request import Request, urlopen


def request(url, data=None, headers=None):
    payload = json.dumps(data).encode() if data is not None else None
    req = Request(url, data=payload, headers={"Content-Type": "application/json", **(headers or {})})
    with urlopen(req, timeout=20) as response:
        return response.status, json.load(response)


def distribution(values):
    values = sorted(values)
    return {
        "p" + str(p): round(values[math.ceil(len(values) * p / 100) - 1], 2)
        for p in [50, 95, 99]
    } if values else {}


def run(args):
    label = uuid.uuid4().hex
    tenants = [str(uuid.uuid4()) for _ in range(math.ceil(args.count / 80))]
    user = str(uuid.uuid4())
    _, health = request("http://127.0.0.1:9080/healthz")
    if health.get("fixture") != "nimbus-local-webhook":
        raise RuntimeError("expected local Compose webhook fixture")

    def create(seq):
        tenant = tenants[seq % len(tenants)]
        body = {"tenant_id": tenant, "user_id": user, "channel": "webhook",
                "payload": {"url": "http://webhook:9080/webhooks/" + label,
                            "body": {"run": label, "seq": seq, "padding": "x" * 128}}}
        start = time.time_ns()
        try:
            status, result = request("http://127.0.0.1:8080/v1/notifications", body,
                                     {"Idempotency-Key": f"{label}-{seq}", "X-Tenant-ID": tenant})
            if status != 201:
                raise RuntimeError(f"HTTP {status}")
            notification_id = str(uuid.UUID(result["id"]))
            return notification_id, seq, tenant, start, (time.time_ns() - start) / 1e6, None
        except Exception as err:
            return None, seq, tenant, start, None, str(err)

    def sql(statement):
        return subprocess.check_output(
            ["docker", "compose", "-p", "nimbus-perf", "exec", "-T", "postgres",
             "psql", "-XAt", "-v", "ON_ERROR_STOP=1", "-U", "nimbus", "-d", "nimbus", "-c", statement],
            text=True, timeout=15).strip()

    where = f"payload->'body'->>'run'='{label}'"
    def counts():
        rows = sql(f"SELECT status, count(*) FROM notifications WHERE {where} GROUP BY status")
        return {status: int(count) for status, count in (row.split('|') for row in rows.splitlines())}

    start_ns = time.time_ns()
    with ThreadPoolExecutor(max_workers=args.clients) as clients:
        replies = list(clients.map(create, range(args.count)))
    ingress_s = (time.time_ns() - start_ns) / 1e9
    accepted = {row[0]: row for row in replies if row[0]}
    errors = Counter(row[-1] for row in replies if row[-1])
    first_sample = counts()
    state = first_sample
    deadline = time.monotonic() + args.timeout
    while state.get("sent", 0) < len(accepted) and time.monotonic() < deadline:
        time.sleep(1)
        state = counts()

    deliveries = request("http://127.0.0.1:9080/scenarios/" + label)[1]["deliveries"] if accepted else []
    seen = Counter()
    latencies = []
    last = start_ns
    invalid = 0
    for receipt in deliveries:
        ident = receipt["notification_id"]
        seen[ident] += 1
        posted = accepted.get(ident)
        if (not posted or receipt["status"] != 200 or receipt["tenant_id"] != posted[2]
                or receipt["body"] != {"seq": posted[1], "run": label, "padding": "x" * 128}):
            invalid += 1
            continue
        received = round(datetime.fromisoformat(receipt["received_at"]).timestamp() * 1e9)
        latencies.append((received - posted[3]) / 1e6)
        last = max(last, received)
    db_rows = sql(f"SELECT id::text, status, attempt FROM notifications WHERE {where}").splitlines()
    persisted = {row.split('|')[0] for row in db_rows}
    elapsed = (last - start_ns) / 1e9
    result = {
        "variant": args.variant, "requested": args.count, "clients": args.clients, "tenants": len(tenants),
        "accepted_responses": sum(row[0] is not None for row in replies), "unique_accepted_ids": len(accepted),
        "errors": dict(errors), "ingress_s": round(ingress_s, 3),
        "accepted_per_s": round(len(accepted) / ingress_s, 2),
        "api_ack_ms": distribution([row[4] for row in replies if row[0]]),
        "first_db_sample_after_ingress": first_sample, "final_db_counts": state, "db_rows": len(db_rows),
        "db_id_mismatches": len(persisted ^ accepted.keys()),
        "db_rows_without_one_sent_attempt": sum(row.split('|')[1:] != ["sent", "1"] for row in db_rows),
        "observed_webhooks": len(deliveries), "missing_at_receiver": len(accepted.keys() - seen.keys()),
        "duplicate_receipts": sum(count-1 for count in seen.values()), "invalid_receipts": invalid,
        "first_submission_to_last_receipt_s": round(elapsed, 3),
        "completed_webhooks_per_s": round(len(latencies) / elapsed, 2) if elapsed else None,
        "submission_to_webhook_ms": distribution(latencies),
    }
    print(json.dumps(result), flush=True)
    with Path(args.output).open("a") as f:
        f.write(json.dumps(result) + "\n")
    if (args.count != len(accepted) or len(deliveries) != args.count or
            any(result[key] for key in ("missing_at_receiver", "duplicate_receipts", "invalid_receipts",
                                       "db_id_mismatches", "db_rows_without_one_sent_attempt"))):
        raise RuntimeError("load trial failed reconciliation")


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--count", type=int, required=True)
    p.add_argument("--variant", required=True)
    p.add_argument("--clients", type=int, default=32)
    p.add_argument("--timeout", type=int, default=240)
    p.add_argument("--output", required=True)
    args = p.parse_args()
    if not 1 <= args.count <= 100_000 or not 1 <= args.clients <= 128:
        p.error("use 1..100000 messages and 1..128 clients")
    run(args)
