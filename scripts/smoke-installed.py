#!/usr/bin/env python3
"""Probe an actual installed server with disposable state, never a live profile."""

import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def probe(command, version, timeout=45):
    with tempfile.TemporaryDirectory(prefix="ori-installer-smoke-") as temp:
        root = Path(temp)
        home = root / "home"
        home.mkdir(mode=0o700)
        # Do not pass release credentials, API keys, or operator config overrides
        # into the application being tested. Retain only OS execution essentials.
        allowed = {"PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TMP", "TEMP", "TMPDIR", "LANG"}
        env = {key: value for key, value in os.environ.items() if key.upper() in allowed}
        env.update(HOME=str(home), USERPROFILE=str(home), ORI_DATA_DIR=str(root / "data"),
                   XDG_CONFIG_HOME=str(home / "config"), XDG_DATA_HOME=str(home / "data"),
                   XDG_CACHE_HOME=str(home / "cache"), APPDATA=str(home / "appdata"),
                   LOCALAPPDATA=str(home / "localappdata"))
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        # A machine's HTTP proxy must never intercept this loopback-only probe.
        http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with (root / "server.log").open("w+", encoding="utf-8") as log:
            process = subprocess.Popen([*command, f"--port={port}"], cwd=root, env=env,
                                       stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + timeout
                while True:
                    if process.poll() is not None:
                        raise RuntimeError(f"Installed server exited with {process.returncode}")
                    try:
                        with http.open(f"http://127.0.0.1:{port}/health", timeout=1) as response:
                            health = json.load(response)
                        if health.get("status") != "ok":
                            raise RuntimeError(f"Unhealthy response: {health}")
                        break
                    except (urllib.error.URLError, TimeoutError):
                        if time.monotonic() >= deadline:
                            raise RuntimeError("Installed server did not become healthy") from None
                        time.sleep(0.1)
                with http.open(f"http://127.0.0.1:{port}/api/updates/version", timeout=5) as response:
                    actual = json.load(response).get("version", "")
                if actual.removeprefix("v") != version.removeprefix("v"):
                    raise RuntimeError(f"Installed version {actual!r} does not match {version!r}")
                if process.poll() is not None:
                    raise RuntimeError("Installed server exited during the probe")
                print(f"Installed {version}: healthy, exact embedded version verified")
            except Exception:
                log.flush()
                log.seek(0)
                print(log.read()[-16000:], file=sys.stderr)
                raise
            finally:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=10)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("version")
    args = parser.parse_args()
    probe([str(args.binary.resolve(strict=True))], args.version)


if __name__ == "__main__":
    main()
