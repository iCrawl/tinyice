#!/usr/bin/env python3
"""Drive a disposable TinyIce instance through its public HTTP interface."""

import argparse
import hashlib
import http.cookiejar
import json
import math
import os
from pathlib import Path
import re
import signal
import socket
import struct
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import wave


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def identity(pid):
    return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]


def owns_process(run):
    try:
        pid = run["pid"]
        return (identity(pid) == run["process_start"]
                and Path(f"/proc/{pid}/exe").resolve() == Path(run["binary"]).resolve())
    except FileNotFoundError:
        return False


def owns_port(run):
    inodes = set()
    for table in ("tcp", "tcp6"):
        for row in Path(f"/proc/net/{table}").read_text().splitlines()[1:]:
            fields = row.split()
            if int(fields[1].split(":")[1], 16) == run["port"] and fields[3] == "0A":
                inodes.add(f"socket:[{fields[9]}]")
    try:
        return bool(inodes & {os.readlink(fd) for fd in Path(f"/proc/{run['pid']}/fd").iterdir()})
    except FileNotFoundError:
        return False


class Client:
    def __init__(self, run, evidence=True):
        self.run = run
        self.root = Path(run["run_dir"])
        self.evidence = evidence
        self.cookies = http.cookiejar.MozillaCookieJar(str(self.root / "cookies.txt"))
        if Path(self.cookies.filename).exists():
            self.cookies.load(ignore_discard=True)
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
        self.csrf = ""

    def request(self, method, path, body=None, form=False, expected=200):
        headers = {"Accept": "application/json"}
        data = None
        if body is not None:
            headers["Content-Type"] = "application/x-www-form-urlencoded" if form else "application/json"
            data = (urllib.parse.urlencode(body) if form else json.dumps(body)).encode()
        if self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        request = urllib.request.Request(self.run["url"] + path, data, headers, method=method)
        try:
            response = self.opener.open(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            status = response.status
            try:
                value = json.loads(raw)
            except json.JSONDecodeError:
                value = raw.decode(errors="replace")
            if self.evidence:
                records = self.root / "responses.jsonl"
                with records.open("a") as out:
                    secret = path.startswith(("/setup", "/login"))
                    evidence_value = value
                    if path.startswith("/assets/"):
                        evidence_value = {"bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()}
                    request_body = {key: "[redacted]" if key in ("password", "token") else item
                                    for key, item in (body or {}).items()}
                    out.write(json.dumps({"time": time.time(), "method": method, "path": path,
                                          "request_body": request_body,
                                          "status": status, "body": "[private auth response]" if secret else evidence_value}) + "\n")
                self.cookies.save(ignore_discard=True)
        assert status == expected, f"{method} {path}: HTTP {status}, expected {expected}"
        return value

    def admin(self):
        html = self.request("GET", "/admin")
        match = re.search(r'window\.__TINYICE__=(.*?)</script>', html)
        assert match, "Admin shell missing initial page data"
        data = json.loads(match[1])
        assert data["user"]["role"] == "superadmin", "Admin session is not usable"
        self.csrf = data["csrfToken"]
        assert self.csrf, "Session has no CSRF token"
        return html


def cleanup(run):
    if owns_process(run):
        os.kill(run["pid"], signal.SIGTERM)
        for _ in range(100):
            if not owns_process(run):
                break
            time.sleep(0.1)
        if owns_process(run):
            raise RuntimeError("Owned process did not shut down; evidence preserved")
    assert not owns_port(run), "Owned HTTP port remains open"
    save(Path(run["run_dir"]) / "cleanup.json", {"stopped": True, "evidence_preserved": True})


def launch(args):
    root = Path(tempfile.mkdtemp(prefix="tinyice-verify-", dir=args.evidence_dir)).resolve()
    os.chmod(root, 0o700)
    (root / "data").mkdir()
    repo = Path(__file__).resolve().parents[4]
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
    dirty = subprocess.check_output(["git", "status", "--porcelain"], cwd=repo, text=True)
    binary = Path(args.binary).resolve() if args.binary else root / "tinyice"
    if not args.binary:
        with (root / "build.log").open("w") as log:
            subprocess.run([args.go, "build", "-ldflags", f"-X main.Commit={revision}",
                            "-o", str(binary), "."], cwd=repo, stdout=log, stderr=log, check=True)
    version = subprocess.check_output([str(binary), "--version"], text=True).strip()
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", args.port))
        port = probe.getsockname()[1]
    run = {"run_dir": str(root), "repo": str(repo), "revision": revision, "working_tree": dirty,
           "binary": str(binary), "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
           "version": version, "port": port, "url": f"http://127.0.0.1:{port}"}
    credentials = {"username": "verify_admin", "password": "verify-local-only-14"}
    save(root / "credentials.json", credentials)
    with (root / "server.log").open("w") as log:
        proc = subprocess.Popen([str(binary), "--host", "127.0.0.1", "--port", str(port),
                                 "--config", str(root / "data/tinyice.json"),
                                 "--pid-file", str(root / "data/tinyice.pid")],
                                cwd=root / "data", stdout=log, stderr=log, start_new_session=True)
    run.update(pid=proc.pid, process_start=identity(proc.pid))
    manifest = root / "run.json"
    save(manifest, run)
    print(manifest, flush=True)
    try:
        client = Client(run)
        for _ in range(100):
            if proc.poll() is not None:
                raise RuntimeError("TinyIce exited; inspect server.log")
            token = re.search(r"Setup Token: (\w+)", (root / "server.log").read_text())
            if token and owns_port(run):
                break
            time.sleep(0.1)
        else:
            raise RuntimeError("Setup readiness timed out; inspect server.log")
        client.request("GET", "/setup")
        client.request("POST", "/setup/verify-token", {"token": token[1]})
        client.request("POST", "/setup/complete", {"token": token[1], **credentials})
        client.request("POST", "/login", credentials, form=True)
        html = client.admin()
        assets = re.findall(r'(?:src|href)="(/assets/[^\"]+)"', html)
        assert assets, "Admin shell has no embedded assets"
        for asset in assets:
            client.request("GET", asset)
        assert isinstance(client.request("GET", "/api/streams"), list)
        save(root / "ready.json", {"admin_usable": True, "embedded_assets": assets})
    except BaseException:
        cleanup(run)
        raise


def doctor(run):
    assert owns_process(run), "PID identity or executable does not match this run"
    assert owns_port(run), "HTTP port is not owned by this run"
    binary = Path(run["binary"])
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == run["binary_sha256"], "Binary changed"
    assert subprocess.check_output([str(binary), "--version"], text=True).strip() == run["version"]
    client = Client(run, evidence=False)
    client.admin()
    assert isinstance(client.request("GET", "/api/streams"), list)
    print(json.dumps({"ready": True, "version": run["version"], "url": run["url"]}))


def smoke(run):
    doctor(run)
    client = Client(run)
    client.admin()
    root = Path(run["run_dir"])
    mount = "/verify-mount"
    client.request("POST", "/api/streams", {"mount": mount, "password": "verify-source-only", "burst_size": 131072})
    client.request("PUT", "/api/streams", {"mount": mount, "visible": True})
    row = next(s for s in client.request("GET", "/api/streams") if s["mount"] == mount)
    assert row["visible"] is True and row["enabled"] is True, "Partial edit changed enabled state"
    config = json.loads((root / "data/tinyice.json").read_text())
    assert config["setup_complete"] and config["visible_mounts"][mount]
    assert config["advanced_mounts"][mount]["burst_size"] == 131072
    music = root / "data/music"
    music.mkdir(exist_ok=True)
    for name, frequency in (("tone-a.wav", 440), ("tone-b.wav", 660)):
        with wave.open(str(music / name), "wb") as wav:
            wav.setparams((2, 2, 44100, 0, "NONE", "not compressed"))
            frames = bytearray()
            for i in range(44100 * 30):
                value = round(8000 * math.sin(2 * math.pi * frequency * i / 44100))
                frames.extend(struct.pack("<hh", value, value))
            wav.writeframes(frames)
    client.request("POST", "/api/autodj", {"name": "Verification WAV", "mount": "/verify-radio",
                   "music_dir": str(music), "format": "mp3", "bitrate": 128,
                   "loop": True, "inject_metadata": True, "visible": True})
    path = "/api/autodj/%2Fverify-radio/"
    files = client.request("GET", path + "files")
    assert {f["name"] for f in files if not f["is_dir"]} >= {"tone-a.wav", "tone-b.wav"}
    playlist = client.request("GET", path + "playlist")
    assert len(playlist) == 2 and all(isinstance(p["id"], int) for p in playlist)
    with urllib.request.urlopen(run["url"] + "/verify-radio", timeout=10) as audio:
        chunk = audio.read(4096)
        assert len(chunk) == 4096 and audio.headers.get_content_type() == "audio/mpeg"
        (root / "audio-prefix.mp3").write_bytes(chunk)
    volume = client.request("POST", path + "volume", {"volume": 1, "unit": "percent"})
    assert volume["volume"] == 0.01, "One percent volume became full volume"
    client.request("POST", path + "pause", {})
    row = next(s for s in client.request("GET", "/api/autodj") if s["mount"] == "/verify-radio")
    assert row["state"] == 2, "Pause did not report numeric paused state"
    client.request("POST", path + "play", {})
    row = next(s for s in client.request("GET", "/api/autodj") if s["mount"] == "/verify-radio")
    assert row["state"] == 1 and row["loop"] is True
    config = json.loads((root / "data/tinyice.json").read_text())
    assert any(a["mount"] == "/verify-radio" and a["loop"] for a in config["autodjs"])
    save(root / "smoke.json", {"mode": "baseline", "mount_partial_edit": True, "config_persisted": True,
                              "wav_autodj": True, "audio_bytes": len(chunk), "pause_play": True,
                              "percent_volume": True, "retained_features_verified": False})
    print(root / "smoke.json")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    start = commands.add_parser("launch")
    start.add_argument("--go", default="go")
    start.add_argument("--binary", help="Existing compiled binary; build identity is recorded")
    start.add_argument("--port", type=int, default=0)
    start.add_argument("--evidence-dir", default=tempfile.gettempdir())
    for command in ("doctor", "smoke", "cleanup"):
        commands.add_parser(command).add_argument("manifest", type=Path)
    args = parser.parse_args()
    if args.command == "launch":
        launch(args)
    else:
        run = json.loads(args.manifest.read_text())
        {"doctor": doctor, "smoke": smoke, "cleanup": cleanup}[args.command](run)


if __name__ == "__main__":
    main()
