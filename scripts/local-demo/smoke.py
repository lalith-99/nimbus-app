#!/usr/bin/env python3
"""Exercise the local webhook stack. Never point this state-changing fixture at production."""

import json
import os
import sys
import time
import uuid
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen


def local_url(variable, default):
    value = os.environ.get(variable, default).rstrip("/")
    parsed = urlsplit(value)
    if (parsed.scheme != "http" or parsed.hostname not in
            {"localhost", "127.0.0.1", "::1", "gateway", "webhook"} or
            parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path):
        raise ValueError(f"{variable} must be a local HTTP origin (this smoke test changes retry state)")
    return value


class Smoke:
    def __init__(self):
        self.api = local_url("NIMBUS_BASE_URL", "http://127.0.0.1:8080")
        self.receiver = local_url("NIMBUS_WEBHOOK_CONTROL_URL", "http://127.0.0.1:9080")
        # The destination is resolved by the gateway, not by this smoke client.
        self.delivery = local_url("NIMBUS_WEBHOOK_DELIVERY_URL", "http://webhook:9080")
        self.tenant = str(uuid.uuid4())
        self.user = str(uuid.uuid4())
        self.run_id = uuid.uuid4().hex
        self.deadline = time.monotonic() + 120

    def request(self, origin, path, method="GET", body=None, expected=200, headers=None):
        if time.monotonic() >= self.deadline:
            raise RuntimeError("smoke test exceeded its 120-second total deadline")
        request_headers = {"Content-Type": "application/json"}
        if origin == self.api:
            request_headers["X-Tenant-ID"] = self.tenant
        request_headers.update(headers or {})
        data = json.dumps(body).encode() if body is not None else None
        request = Request(origin + path, data=data, headers=request_headers, method=method)
        try:
            with urlopen(request, timeout=3) as response:
                status, response_headers, raw = response.status, response.headers, response.read()
        except HTTPError as response:
            status, response_headers, raw = response.code, response.headers, response.read()
        if status != expected:
            raise RuntimeError(f"{method} {path}: expected {expected}, got {status}: {raw.decode(errors='replace')[:500]}")
        try:
            result = json.loads(raw)
        except ValueError:
            result = raw.decode(errors="replace")
        return result, response_headers

    def wait_for(self, label, fetch, predicate, timeout=25):
        end = min(time.monotonic() + timeout, self.deadline)
        last = None
        while time.monotonic() < end:
            last = fetch()
            if predicate(last):
                return last
            time.sleep(1)
        raise RuntimeError(f"timed out waiting for {label}; last value: {last}")

    def notification(self, notification_id):
        return self.request(self.api, f"/v1/notifications/{notification_id}")[0]

    def scenario(self, name, failures):
        self.request(self.receiver, f"/scenarios/{name}", "PUT", {"failures": failures})

    def deliveries(self, name, notification_id):
        state = self.request(self.receiver, f"/scenarios/{name}")[0]
        return [item for item in state["deliveries"] if item["notification_id"] == notification_id]

    def create(self, name, key):
        body = {
            "tenant_id": self.tenant,
            "user_id": self.user,
            "channel": "webhook",
            "payload": {
                "url": f"{self.delivery}/webhooks/{name}",
                "body": {"message": "Nimbus local demo", "run_id": self.run_id},
            },
        }
        result, _ = self.request(self.api, "/v1/notifications", "POST", body, 201,
                                 {"Idempotency-Key": key})
        uuid.UUID(result["id"])
        return result["id"], body

    def wait_status(self, notification_id, status, attempt=None):
        return self.wait_for(
            f"{notification_id} to become {status}",
            lambda: self.notification(notification_id),
            lambda item: item["status"] == status and (attempt is None or item["attempt"] == attempt),
        )

    def advance_retry_fixture(self, notification, attempt):
        print(f"LOCAL FIXTURE: clear scheduled retry time and set attempt={attempt} for {notification['id']}.", flush=True)
        print(f"  Original next_retry_at: {notification.get('next_retry_at')}; this skips backoff waiting, not delivery work.", flush=True)
        # This existing administrative API clears next_retry_at. It is used only
        # after observing a completed failed attempt, never while a worker is sending.
        self.request(self.api, f"/v1/notifications/{notification['id']}/status", "PATCH", {
            "status": "pending",
            "attempt": attempt,
            "error": notification.get("error_message"),
        })

    def run(self):
        self.request(self.api, "/health")
        fixture, _ = self.request(self.receiver, "/healthz")
        if fixture.get("fixture") != "nimbus-local-webhook":
            raise RuntimeError("receiver is not the Nimbus local fixture")
        print(f"Local smoke run {self.run_id}; tenant {self.tenant}", flush=True)

        success = "success-" + self.run_id
        self.scenario(success, 0)
        key = "local-demo-" + self.run_id
        notification_id, body = self.create(success, key)
        replay, headers = self.request(self.api, "/v1/notifications", "POST", body, 201,
                                       {"Idempotency-Key": key})
        assert replay["id"] == notification_id, "replay created a second notification"
        assert headers.get("X-Idempotency-Replayed") == "true", "missing Redis replay header"
        self.wait_status(notification_id, "sent", 1)
        assert [item["status"] for item in self.deliveries(success, notification_id)] == [200]
        print("PASS: create → sent; replay returned the same ID; one webhook delivery observed.", flush=True)

        retry = "retry-" + self.run_id
        self.scenario(retry, 1)
        retry_id, _ = self.create(retry, key + "-retry")
        failed = self.wait_status(retry_id, "pending", 1)
        assert failed.get("next_retry_at"), "failed delivery did not schedule a retry"
        assert [item["status"] for item in self.deliveries(retry, retry_id)] == [503]
        self.advance_retry_fixture(failed, attempt=1)
        self.wait_status(retry_id, "sent", 2)
        assert [item["status"] for item in self.deliveries(retry, retry_id)] == [503, 200]
        print("PASS: actual 503 → scheduled retry → actual 200 (backoff time skipped by local fixture).", flush=True)

        exhausted = "dlq-" + self.run_id
        self.scenario(exhausted, -1)
        original_id, _ = self.create(exhausted, key + "-dlq")
        failed = self.wait_status(original_id, "pending", 1)
        print("LOCAL FIXTURE: seed attempt=4; the next real failure exercises the five-attempt limit.", flush=True)
        self.advance_retry_fixture(failed, attempt=4)
        self.wait_status(original_id, "dead_lettered")
        queue, _ = self.request(self.api, f"/v1/dlq?tenant_id={self.tenant}")
        matches = [item for item in queue["data"] if item["original_notification_id"] == original_id]
        assert len(matches) == 1, "expected exactly one DLQ record for the failed notification"
        dead_letter = matches[0]
        assert dead_letter["status"] == "pending" and dead_letter["attempts"] == 5
        assert [item["status"] for item in self.deliveries(exhausted, original_id)] == [503, 503]
        print("PASS: worker moved notification to DLQ; two real 503s observed, attempt count seeded to 4.", flush=True)

        self.scenario(exhausted, 0)
        recovered, _ = self.request(self.api, f"/v1/dlq/{dead_letter['id']}/retry", "POST")
        recovery_id = recovered["new_notification_id"]
        assert recovery_id != original_id, "DLQ recovery should create a fresh notification"
        self.wait_status(recovery_id, "sent", 1)
        updated, _ = self.request(self.api, f"/v1/dlq/{dead_letter['id']}")
        assert updated["status"] == "retried" and updated["retried_notification_id"] == recovery_id
        assert [item["status"] for item in self.deliveries(exhausted, recovery_id)] == [200]
        print("PASS: DLQ retry → new notification → sent; original DLQ item marked retried.", flush=True)
        print("PASS: all local smoke checks completed. This is functional evidence, not a load test or exactly-once guarantee.", flush=True)


if __name__ == "__main__":
    try:
        Smoke().run()
    except (AssertionError, KeyError, RuntimeError, TypeError, URLError, ValueError, TimeoutError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr, flush=True)
        sys.exit(1)
