#!/usr/bin/env python3
"""Verify actual application metrics, correlated logs/traces and Grafana provisioning."""
import base64
import json
import os
import time
import urllib.parse
import urllib.request

def get(base, path, params=None, auth=False):
    query = "?" + urllib.parse.urlencode(params) if params else ""
    request = urllib.request.Request(base + path + query)
    if auth:
        password = os.environ["GRAFANA_ADMIN_PASSWORD"]
        request.add_header("Authorization", "Basic " + base64.b64encode(("admin:" + password).encode()).decode())
    with urllib.request.urlopen(request, timeout=5) as response:
        return json.load(response)

def verify():
    metrics = get("http://127.0.0.1:9090", "/api/v1/query", {"query": 'election_poll_total{service_name="election-bot"}'})
    assert metrics["status"] == "success" and metrics["data"]["result"], "application metrics absent"
    logs = get("http://127.0.0.1:3100", "/loki/api/v1/query_range", {"query": '{service_name="election-bot"}', "start": str(time.time_ns()-1800*10**9), "limit": "1000"})
    assert logs["data"]["result"], "Loki logs absent"
    trace_id = None
    for stream in logs["data"]["result"]:
        for entry in stream["values"]:
            metadata = entry[2] if len(entry) > 2 else {}
            candidate = metadata.get("trace_id") or stream["stream"].get("trace_id")
            if candidate:
                trace_id = candidate
                break
        if trace_id:
            break
    assert trace_id, "correlated log trace ID absent; run OTEL_ENABLED=true make simulate"
    trace = get("http://127.0.0.1:3200", "/api/traces/" + trace_id)
    assert trace.get("batches") or trace.get("resourceSpans"), "Tempo trace retrieval empty"
    datasources = get("http://127.0.0.1:3000", "/api/datasources", auth=True)
    assert {"prometheus", "tempo", "loki"}.issubset({x["uid"] for x in datasources}), "datasources absent"
    for uid in ("election-bot-overview", "election-bot-telemetry"):
        dashboard = get("http://127.0.0.1:3000", "/api/dashboards/uid/" + uid, auth=True)
        assert dashboard["dashboard"]["uid"] == uid, "dashboard absent"
    availability = get("http://127.0.0.1:9090", "/api/v1/query", {"query": 'probe_success{job="application-readiness"}'})
    assert any(float(x["value"][1]) == 1 for x in availability["data"]["result"]), "readiness probe failed"
    print("PASS: Prometheus metrics, Loki correlated logs, Tempo trace " + trace_id + ", Grafana dashboards, readiness")

if __name__ == "__main__":
    deadline = time.monotonic() + 60
    last = None
    while time.monotonic() < deadline:
        try:
            verify()
            break
        except (AssertionError, OSError, KeyError) as error:
            last = error
            time.sleep(2)
    else:
        raise SystemExit("FAIL: " + str(last))
