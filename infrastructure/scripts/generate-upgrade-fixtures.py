#!/usr/bin/env python3
"""Drive only the loopback historical-build binary supplied by generate-upgrade-fixtures.sh."""
import json
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone

milestone, output = int(sys.argv[1]), sys.argv[2]
base = "http://127.0.0.1:18082"

def request(method, path, data=None):
    body = json.dumps(data).encode() if data is not None else None
    req = urllib.request.Request(base + path, body, {"Content-Type": "application/json"}, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            payload = response.read()
            return response.status, json.loads(payload) if payload else {}
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read() or b"{}")

def fixture(method, path, data=None):
    req = urllib.request.Request("http://127.0.0.1:18091" + path, json.dumps(data).encode() if data else None,
                                 {"Content-Type": "application/json"}, method=method)
    with urllib.request.urlopen(req, timeout=5):
        pass

def good(method, path, data=None):
    code, result = request(method, path, data)
    if code < 200 or code >= 300:
        raise RuntimeError(f"{method} {path}: {code} {result}")
    return result

def monitor(name, url="healthy", **extra):
    body = {"name": name, "check": {"url": "http://127.0.0.1:18090/" + url, "expectedStatus": 200,
                                      "deadlineMs": 1000}}
    if milestone >= 2 and "heartbeat" not in body:
        body["intervalSeconds"] = 10
    body.update(extra)
    return good("POST", "/api/monitors", body)

for _ in range(60):
    try:
        if request("GET", "/api/health/live")[0] == 200:
            break
    except (urllib.error.URLError, TimeoutError):
        pass
    time.sleep(.2)
else:
    raise RuntimeError("historical API failed to start")

policy = {"incidentPolicy": {"openAfter": 1, "recoverAfter": 1}} if milestone >= 3 else {}
a = monitor(f"M{milestone} checks", **policy)
mid = a["id"]
good("POST", f"/api/monitors/{mid}/checks", {})
if milestone >= 3:
    fixture("PUT", "/control/mode", {"mode": "fail"})
    failing = monitor(f"M{milestone} incident", "failing", **policy)
    fid = failing["id"]
    good("POST", f"/api/monitors/{fid}/checks", {})
    for _ in range(35):
        current = good("GET", f"/api/monitors/{fid}/incidents?limit=10")["incidents"]
        if current:
            states = [n["state"] for n in good("GET", f"/api/monitors/{fid}/incidents/{current[0]['id']}")["notifications"]]
            if "failed" in states:
                break
        time.sleep(1)
    else:
        raise RuntimeError("fixture notification did not reach final failed state")
    fixture("PUT", "/control/mode", {"mode": "accept"})
    good("PATCH", f"/api/monitors/{fid}", {"expectedConfigVersion": 1,
        "check": {"url": "http://127.0.0.1:18090/healthy"}})
    good("POST", f"/api/monitors/{fid}/checks", {})
    opened = monitor(f"M{milestone} open", "failing", **policy)
    good("POST", f"/api/monitors/{opened['id']}/checks", {})
    t = datetime.now(timezone.utc)
    start = (t - timedelta(seconds=1)).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    end = (t + timedelta(seconds=1)).isoformat(timespec="milliseconds").replace("+00:00", "Z")
    window = good("POST", f"/api/monitors/{mid}/maintenance", {"startAt": start, "endAt": end,
                                                                "note": "upgrade fixture"})
    time.sleep(1.5)
    good("POST", f"/api/monitors/{mid}/lifecycle", {"action": "pause"})
    good("POST", f"/api/monitors/{mid}/lifecycle", {"action": "resume"})
else:
    good("POST", f"/api/monitors/{mid}/lifecycle", {"action": "pause"})
    good("POST", f"/api/monitors/{mid}/lifecycle", {"action": "resume"})
paused = monitor(f"M{milestone} paused")
good("POST", f"/api/monitors/{paused['id']}/lifecycle", {"action": "pause"})
archive = monitor(f"M{milestone} archived")
good("POST", f"/api/monitors/{archive['id']}/lifecycle", {"action": "archive"})
if milestone >= 4:
    hb = good("POST", "/api/monitors", {"kind": "heartbeat", "name": f"M{milestone} heartbeat",
        "heartbeat": {"intervalSeconds": 10, "graceSeconds": 5}, **policy})
    token = hb.get("issuedToken")
    if token:
        req = urllib.request.Request(base + f"/ingest/heartbeats/{hb['id']}",
            json.dumps({"runId": f"m{milestone}-fixture-run"}).encode(),
            {"Authorization": "Bearer " + token, "Content-Type": "application/json"}, method="POST")
        with urllib.request.urlopen(req, timeout=5):
            pass
    app = good("POST", "/api/applications", {"name": f"M{milestone} service"})
    good("PUT", f"/api/monitors/{mid}/application", {"applicationId": app["id"]})
    for i in (1, 2):
        good("POST", f"/api/applications/{app['id']}/deployments", {"version": f"fixture-{i}",
            "deploymentId": f"m{milestone}-deployment-{i}"})
    time.sleep(17)

if len(sys.argv) > 3 and sys.argv[3] == "--scene-only":
    if milestone == 2:
        for _ in range(20):
            observations = good("GET", f"/api/monitors/{mid}/observations?limit=200")["observations"]
            if any(o.get("initiatedBy") == "scheduled" and o.get("counted") for o in observations):
                break
            time.sleep(1)
        else:
            raise RuntimeError("scene 2 missing counted scheduled check")
    sys.exit(0)
facts = {"milestone": milestone, "monitors": [], "applications": []}
for m in good("GET", "/api/monitors")["monitors"]:
    ident = m["id"]
    obs = good("GET", f"/api/monitors/{ident}/observations?limit=200")["observations"]
    row = {"id": ident, "name": m["name"], "kind": m.get("kind", "http"),
           "lifecycle": m["lifecycle"], "observationCount": len(obs), "incidents": []}
    if milestone >= 3:
        for incident in good("GET", f"/api/monitors/{ident}/incidents?limit=200")["incidents"]:
            detail = good("GET", f"/api/monitors/{ident}/incidents/{incident['id']}")
            row["incidents"].append({"id": incident["id"], "state": incident["state"],
                "resolution": incident.get("resolution"), "notificationStates":
                    sorted(n["state"] for n in detail["notifications"])})
    facts["monitors"].append(row)
if milestone >= 4:
    for a in good("GET", "/api/applications")["applications"]:
        markers = good("GET", f"/api/applications/{a['id']}/deployments?limit=200")["deployments"]
        facts["applications"].append({"id": a["id"], "name": a["name"],
                                       "markerIds": sorted(marker["id"] for marker in markers)})
with open(output, "w", encoding="utf8") as f:
    json.dump(facts, f, sort_keys=True, indent=2)
    f.write("\n")
