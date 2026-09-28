#!/usr/bin/env python3
"""Capture milestone API facts after the historical scheduler has stopped."""
import json
import sys
import time
import urllib.error
import urllib.request

milestone, output = int(sys.argv[1]), sys.argv[2]
base = "http://127.0.0.1:18082"

def good(path):
    with urllib.request.urlopen(base + path, timeout=15) as response:
        return json.load(response)

for attempt in range(60):
    try:
        good("/api/health/live")
        break
    except (urllib.error.URLError, TimeoutError):
        time.sleep(.2)
else:
    raise RuntimeError("historical API failed to start for facts")

if len(sys.argv) > 3 and sys.argv[3] == "--wait-gap":
    for attempt in range(30):
        for monitor in good("/api/monitors")["monitors"]:
            if good(f"/api/monitors/{monitor['id']}/gaps?limit=200")["gaps"]:
                sys.exit(0)
        time.sleep(1)
    raise RuntimeError("historical scheduler did not record an outage gap")

facts = {"milestone": milestone, "monitors": [], "applications": []}
for m in good("/api/monitors")["monitors"]:
    ident = m["id"]
    observations = good(f"/api/monitors/{ident}/observations?limit=200")["observations"]
    row = {"id": ident, "name": m["name"], "kind": m.get("kind", "http"),
           "lifecycle": m["lifecycle"], "observationCount": len(observations), "incidents": []}
    if milestone >= 2:
        row["gapCount"] = len(good(f"/api/monitors/{ident}/gaps?limit=200")["gaps"])
    if milestone >= 3:
        for incident in good(f"/api/monitors/{ident}/incidents?limit=200")["incidents"]:
            detail = good(f"/api/monitors/{ident}/incidents/{incident['id']}")
            row["incidents"].append({"id": incident["id"], "state": incident["state"],
                "resolution": incident.get("resolution"),
                "notificationStates": sorted(n["state"] for n in detail["notifications"])})
    facts["monitors"].append(row)
if milestone >= 4:
    for a in good("/api/applications")["applications"]:
        markers = good(f"/api/applications/{a['id']}/deployments?limit=200")["deployments"]
        facts["applications"].append({"id": a["id"], "name": a["name"],
                                       "markerIds": sorted(marker["id"] for marker in markers)})
if milestone >= 2 and not any(m.get("gapCount", 0) for m in facts["monitors"]):
    raise RuntimeError("fixture contains no recorded gap")
with open(output, "w", encoding="utf8") as f:
    json.dump(facts, f, sort_keys=True, indent=2)
    f.write("\n")
