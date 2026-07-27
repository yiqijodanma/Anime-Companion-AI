#!/usr/bin/env python3
"""Create a compressed PostgreSQL logical backup and upload it to private OSS."""

from __future__ import annotations

import os
import pathlib
import re
import secrets
import subprocess
import sys
import tempfile
from datetime import datetime, timezone


def required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"{name} is required")
    return value


def validate_configuration(bucket: str, endpoint: str, region: str, prefix: str) -> None:
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,62}", bucket):
        raise RuntimeError("OSS_BUCKET has an invalid format")
    if not re.fullmatch(r"[a-z0-9.-]+", endpoint):
        raise RuntimeError("OSS_ENDPOINT must be a hostname without a URL scheme")
    if not re.fullmatch(r"[a-z0-9-]+", region):
        raise RuntimeError("OSS_REGION has an invalid format")
    if not prefix or prefix.startswith("/") or ".." in prefix or not re.fullmatch(
        r"[A-Za-z0-9._/-]*", prefix
    ):
        raise RuntimeError("OSS_PREFIX contains an unsafe path")


def create_dump(destination: pathlib.Path) -> None:
    env = os.environ.copy()
    env["PGPASSWORD"] = required("PGPASSWORD")
    command = [
        "pg_dump",
        "--host",
        required("PGHOST"),
        "--port",
        os.environ.get("PGPORT", "5432"),
        "--username",
        required("PGUSER"),
        "--dbname",
        required("PGDATABASE"),
        "--format=custom",
        "--compress=9",
        "--no-owner",
        "--no-privileges",
        f"--file={destination}",
    ]
    subprocess.run(command, env=env, check=True)
    if not destination.is_file() or destination.stat().st_size == 0:
        raise RuntimeError("pg_dump produced an empty backup")


def upload_dump(source: pathlib.Path, object_key: str) -> None:
    required("OSS_ACCESS_KEY_ID")
    required("OSS_ACCESS_KEY_SECRET")
    bucket = required("OSS_BUCKET")
    endpoint = required("OSS_ENDPOINT").removeprefix("https://").rstrip("/")
    region = required("OSS_REGION")
    validate_configuration(bucket, endpoint, region, os.environ.get("OSS_PREFIX", ""))

    env = os.environ.copy()
    env["HOME"] = "/tmp"
    env["OSS_ENDPOINT"] = f"https://{endpoint}"
    env["OSS_REGION"] = region
    object_url = f"oss://{bucket}/{object_key}"
    command = [
        "ossutil",
        "cp",
        str(source),
        object_url,
        "--force",
        "--no-progress",
        "--content-type",
        "application/octet-stream",
        "--metadata=x-oss-server-side-encryption:AES256",
        "--bigfile-threshold",
        "100Mi",
        "--part-size",
        "64Mi",
        "--parallel",
        "2",
        "--checkpoint-dir",
        "/tmp/ossutil-checkpoint",
        "--output-dir",
        "/tmp/ossutil-output",
        "--read-timeout",
        "300",
        "--retry-times",
        "10",
        "--loglevel",
        "off",
        "--quiet",
    ]
    subprocess.run(command, env=env, cwd="/tmp", check=True)


def main() -> int:
    os.umask(0o077)
    prefix = os.environ.get("OSS_PREFIX", "anime-companion/postgres").strip("/")
    bucket = required("OSS_BUCKET")
    endpoint = required("OSS_ENDPOINT").removeprefix("https://").rstrip("/")
    region = required("OSS_REGION")
    validate_configuration(bucket, endpoint, region, prefix)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    object_key = f"{prefix}/companion-{timestamp}-{secrets.token_hex(4)}.dump"

    with tempfile.TemporaryDirectory(prefix="anime-companion-backup-") as workdir:
        dump_path = pathlib.Path(workdir) / "companion.dump"
        print("Creating PostgreSQL logical backup", flush=True)
        create_dump(dump_path)
        print(f"Uploading encrypted OSS object {object_key}", flush=True)
        upload_dump(dump_path, object_key)
    print(f"Backup completed: oss://{bucket}/{object_key}", flush=True)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except subprocess.CalledProcessError as exc:
        executable = pathlib.Path(str(exc.cmd[0])).name if exc.cmd else "command"
        print(
            f"Backup failed: {executable} exited with status {exc.returncode}",
            file=sys.stderr,
        )
        raise SystemExit(1)
    except Exception as exc:
        print(f"Backup failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        raise SystemExit(1)
