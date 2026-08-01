#!/usr/bin/env python3
"""Fail a release when the tag and SDK package versions do not match."""

from __future__ import annotations

import json
import re
import sys
import tomllib
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def main() -> int:
    expected = sys.argv[1].strip() if len(sys.argv) > 1 else ""
    expected = expected.removeprefix("v")

    with (ROOT / "adapters/python/pyproject.toml").open("rb") as handle:
        python_version = tomllib.load(handle)["project"]["version"]

    npm_version = json.loads(
        (ROOT / "adapters/typescript/package.json").read_text(encoding="utf-8")
    )["version"]

    init_text = (ROOT / "adapters/python/runeward/__init__.py").read_text(
        encoding="utf-8"
    )
    match = re.search(r'^__version__ = "([^"]+)"$', init_text, re.MULTILINE)
    if match is None:
        raise SystemExit("could not find Python __version__")
    import_version = match.group(1)

    mcp_text = (ROOT / "internal/mcp/mcp.go").read_text(encoding="utf-8")
    mcp_match = re.search(r'^const Version = "([^"]+)"$', mcp_text, re.MULTILINE)
    if mcp_match is None:
        raise SystemExit("could not find MCP implementation version")

    manifest_version = json.loads(
        (ROOT / "dist/mcp/server.json").read_text(encoding="utf-8")
    )["version"]

    chart_text = (ROOT / "deploy/helm/runeward/Chart.yaml").read_text(encoding="utf-8")
    chart_match = re.search(r'^version: ([^\s]+)$', chart_text, re.MULTILINE)
    if chart_match is None:
        raise SystemExit("could not find Helm chart version")

    versions = {
        "Python project": python_version,
        "Python import": import_version,
        "npm package": npm_version,
        "MCP implementation": mcp_match.group(1),
        "MCP manifest": manifest_version,
        "Helm chart": chart_match.group(1),
    }
    if len(set(versions.values())) != 1:
        for label, version in versions.items():
            print(f"{label}: {version}", file=sys.stderr)
        raise SystemExit("SDK package versions do not match")

    actual = python_version
    if expected and actual != expected:
        raise SystemExit(f"SDK version {actual} does not match release version {expected}")

    print(actual)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
