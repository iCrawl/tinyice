#!/usr/bin/env python3
"""Verify retained contracts through a disposable TinyIce HTTP server."""

import argparse
import contextlib
import json
from pathlib import Path
import queue
import threading
import time
import urllib.request

from verify import Client, doctor, save


class Feed:
    def __init__(self, url, events=False):
        self.response = urllib.request.urlopen(url, timeout=20)
        self.events = queue.Queue()
        self.bytes = 0
        self.error = None
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.read, args=(events,), daemon=True)
        self.thread.start()

    def read(self, events):
        try:
            name, data = "message", []
            while not self.stop.is_set():
                if not events:
                    block = self.response.read(1024)
                    if not block:
                        return
                    self.bytes += len(block)
                    continue
                raw = self.response.readline()
                if not raw:
                    return
                line = raw.decode().strip()
                if not line:
                    if data:
                        self.events.put((name, json.loads("\n".join(data))))
                    name, data = "message", []
                elif line.startswith("event:"):
                    name = line[6:].strip()
                elif line.startswith("data:"):
                    data.append(line[5:].strip())
        except (OSError, ValueError, AttributeError) as error:
            if not self.stop.is_set():
                self.error = str(error)

    def wait(self, predicate, timeout=15):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                name, value = self.events.get(timeout=min(1, deadline - time.monotonic()))
            except queue.Empty:
                if self.error:
                    raise AssertionError(self.error)
                continue
            if predicate(name, value):
                return value
        raise AssertionError("Expected SSE event did not arrive")

    def close(self):
        self.stop.set()
        self.response.close()
        self.thread.join(timeout=2)

    def drain(self):
        while True:
            try:
                self.events.get_nowait()
            except queue.Empty:
                return


def eventually(read, predicate, description, timeout=10):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = read()
        if predicate(value):
            return value
        time.sleep(.1)
    raise AssertionError(description)


def integration(run):
    doctor(run)
    client = Client(run)
    client.admin()
    root = Path(run["run_dir"])
    assert (root / "smoke.json").exists(), "Run baseline smoke first"
    source, target = "/acceptance-radio", "/acceptance-target"
    evidence = {"revision": run["revision"], "checks": {}}
    checks = evidence["checks"]
    client.request("POST", "/api/streams", {
        "mount": "/acceptance-disabled", "password": "verify-source-only",
        "enabled": False, "visible": True, "max_listeners": 3, "burst_size": 65536})
    streams = lambda: client.request("GET", "/api/streams")
    row = next(s for s in streams() if s["mount"] == "/acceptance-disabled")
    assert row["enabled"] is False and row["visible"] is True
    assert row["max_listeners"] == 3 and row["effective_max_listeners"] == 3
    config = json.loads((root / "data/tinyice.json").read_text())
    assert config["disabled_mounts"]["/acceptance-disabled"] is True
    assert config["visible_mounts"]["/acceptance-disabled"] is True
    assert config["advanced_mounts"]["/acceptance-disabled"]["max_listeners"] == 3
    checks["create_flags_cap_and_burst_persist"] = row
    for mount in (source, target):
        client.request("POST", "/api/autodj", {
            "name": "Acceptance " + mount, "mount": mount,
            "music_dir": str(root / "data/music"), "format": "mp3", "bitrate": 128,
            "loop": True, "inject_metadata": True, "visible": True})
        client.request("PUT", "/api/streams", {"mount": mount, "max_listeners": 1})
    with contextlib.ExitStack() as stack:
        def feed(path, events=False):
            value = Feed(run["url"] + path, events)
            stack.callback(value.close)
            return value

        metadata = feed("/events/metadata", True)
        combined = feed("/events", True)
        first = metadata.wait(lambda n, v: n == "metadata" and v["mount"] == source and v["title"] != "")
        assert first["playback_id"] and first["revision"] > 0
        assert not first["started_at"].startswith("0001-")
        initial = combined.wait(lambda n, v: n == "stream" and v["mount"] == source and v.get("playback_id") == first["playback_id"])
        assert initial["revision"] == first["revision"]
        path = "/api/autodj/%2Facceptance-radio/"
        client.request("POST", path + "pause", {})
        time.sleep(11)
        paused = next(s for s in streams() if s["mount"] == source)
        assert paused["status"] == "paused" and paused["recovery"]["phase"] not in ("backoff", "recovering")
        combined.drain()
        client.request("POST", path + "play", {})
        eventually(streams, lambda rows: any(s["mount"] == source and s.get("producer_last_progress") != paused.get("producer_last_progress") for s in rows), "Resumed producer did not publish real audio")
        resumed = combined.wait(lambda n, v: n == "stream" and v["mount"] == source and v.get("playback_id") == first["playback_id"])
        assert resumed["started_at"] == first["started_at"]
        checks["pause_keeps_identity_and_avoids_health_recovery"] = paused
        client.request("POST", path + "next", {})
        second = metadata.wait(lambda n, v: n == "metadata" and v["mount"] == source and v["playback_id"] not in ("", first["playback_id"]))
        client.request("POST", path + "next", {})
        repeated = metadata.wait(lambda n, v: n == "metadata" and v["mount"] == source and v["title"] == first["title"] and v["playback_id"] not in ("", first["playback_id"], second["playback_id"]))
        assert first["revision"] < second["revision"] < repeated["revision"]
        checks["real_activation_replay_and_repeated_title_identity"] = [first, second, repeated]
        client.request("PUT", "/api/streams", {"mount": source, "visible": False})
        client.request("POST", path + "next", {})
        hidden = combined.wait(lambda n, v: n == "stream" and v["mount"] == source and v.get("revision", 0) > repeated["revision"] and v.get("playback_id", "") != "")
        checks["unlisted_combined_snapshot_reconciliation"] = hidden
        client.request("PUT", "/api/streams", {"mount": source, "visible": True})
        source_audio = feed(source)
        target_audio = feed(target)
        listeners = lambda: client.request("GET", "/api/listeners")
        active = eventually(listeners, lambda rows: len([r for r in rows if r["current_mount"] in (source, target)]) == 2, "HTTP listeners did not register")
        listener = next(r for r in active if r["current_mount"] == source)
        occupied = next(r for r in active if r["current_mount"] == target)
        client.request("GET", source, expected=503)
        client.request("POST", "/api/listeners/move", {"id": listener["id"], "target_mount": target}, expected=409)
        assert next(r for r in listeners() if r["id"] == listener["id"])["current_mount"] == source
        client.request("POST", "/api/listeners/disconnect", {"id": occupied["id"]})
        eventually(listeners, lambda rows: not any(r["id"] == occupied["id"] for r in rows), "Destination disconnect did not release its slot")
        accepted = client.request("POST", "/api/listeners/move", {"id": listener["id"], "target_mount": target}, expected=202)
        assert accepted["status"] == "pending" and accepted["destination"] == target
        moved = eventually(listeners, lambda rows: any(r["id"] == listener["id"] and r["current_mount"] == target and r.get("move_status") == "completed" for r in rows), "Accepted move did not complete")
        row = next(r for r in moved if r["id"] == listener["id"])
        assert row["move_id"] == accepted["id"]
        bytes_at_move = source_audio.bytes
        eventually(lambda: source_audio.bytes, lambda count: count > bytes_at_move + 4096, "Moved HTTP connection stopped carrying audio")
        client.request("POST", "/api/listeners/disconnect", {"id": listener["id"]})
        eventually(listeners, lambda rows: not any(r["id"] == listener["id"] for r in rows), "Moved listener disconnect did not unregister")
        checks["atomic_cap_full_target_move_completion_disconnect"] = {"accepted": accepted, "completed": row, "audio_bytes": source_audio.bytes}
        client.request("DELETE", "/api/autodj?mount=%2Facceptance-radio", {})
        clear = metadata.wait(lambda n, v: n == "metadata" and v["mount"] == source and v["title"] == "" and v["playback_id"] == "")
        cleared_snapshot = combined.wait(lambda n, v: n == "stream" and v["mount"] == source and v.get("revision") == clear["revision"] and v["title"] == "")
        replay = feed("/events/metadata", True).wait(lambda n, v: n == "metadata" and v["mount"] == source)
        assert replay == clear
        assert clear["revision"] > hidden["revision"]
        checks["terminal_clear_combined_snapshot_and_replay"] = {"clear": clear, "snapshot": cleared_snapshot, "replay": replay}
    client.request("DELETE", "/api/autodj?mount=%2Facceptance-target", {})
    save(root / "integration.json", evidence)
    print(root / "integration.json")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    args = parser.parse_args()
    integration(json.loads(args.manifest.read_text()))
