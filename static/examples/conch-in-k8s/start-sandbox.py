#!/usr/bin/env python3
import os
import subprocess
import sys
import time

sys.path.insert(0, "/opt/conch/sdk")

from conch import Sandbox
from conch.sandbox import CommandExitException


SOURCE_TEMPLATE = os.environ.get("CONCH_TEMPLATE_NAME", "hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4")
SNAPSHOT_TEMPLATE = os.environ.get("CONCH_SNAPSHOT_TEMPLATE", "hub.conch.com:5000/conch/openeuler-snapshot:latest")
CONCH_BIN = os.environ.get("CONCH_BIN", "/usr/local/bin/conch")
CONCH_CONFIG = os.environ.get("CONCH_CONFIG", "/etc/conch/config.yaml")


def template_pull(template_name):
    subprocess.run(
        [CONCH_BIN, "template", "pull", "--plain-http", "--config", CONCH_CONFIG, template_name],
        check=True,
    )


def template_push(template_name):
    subprocess.run(
        [CONCH_BIN, "template", "push", "--plain-http", "--config", CONCH_CONFIG, template_name, template_name],
        check=True,
    )


def start(template_name, sandbox_id):
    begin = time.perf_counter()
    sandbox = Sandbox.create(template_name=template_name, sandbox_id=sandbox_id)
    elapsed = time.perf_counter() - begin
    print(f"{sandbox_id}: {elapsed:.3f}s, ip={sandbox.ip}")
    return sandbox


def run_commands(sandbox, sandbox_id):
    try:
        result = sandbox.commands.run(
            cmd="python3",
            args=["-m", "ensurepip", "--upgrade"],
        )
        print(f"{sandbox_id} ensurepip exit={result.exit_code}")
        result = sandbox.commands.run(
            cmd="python3",
            args=[
                "-m", "pip", "install",
                "-i", "https://mirrors.aliyun.com/pypi/simple/",
                "--trusted-host", "mirrors.aliyun.com",
                "numpy", "sympy", "mpmath",
            ],
        )
        print(f"{sandbox_id} pip install math packages exit={result.exit_code}")
    except CommandExitException as exc:
        print(f"{sandbox_id} command failed: {exc}")


template_pull(SOURCE_TEMPLATE)
first = start(SOURCE_TEMPLATE, "k8s-template-start")
run_commands(first, "k8s-template-start")
try:
    snapshot = first.checkpoint(SNAPSHOT_TEMPLATE)
    print(f"snapshot template: {snapshot.template_name} ({snapshot.template_id})")
    template_push(SNAPSHOT_TEMPLATE)
    template_pull(SNAPSHOT_TEMPLATE)
finally:
    first.delete()

for sandbox_id in ("k8s-snapshot-start-1", "k8s-snapshot-start-2"):
    sandbox = start(snapshot.template_name, sandbox_id)
    run_commands(sandbox, sandbox_id)
    sandbox.delete()
