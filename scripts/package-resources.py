#!/usr/bin/env python3
"""Bundle public documentation, dependency notices, and build provenance."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess


def output(*args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("executable", type=Path)
    parser.add_argument("version")
    parser.add_argument("platform")
    args = parser.parse_args()
    destination = args.destination
    destination.mkdir(parents=True, exist_ok=True)
    shutil.copy2("README.md", destination)
    shutil.copytree("docs", destination / "docs")
    if Path("LICENSE").is_file():
        shutil.copy2("LICENSE", destination)

    licenses = destination / "THIRD_PARTY_LICENSES"
    licenses.mkdir()
    raw = output("go", "list", "-mod=readonly", "-tags=netcgo", "-deps", "-json", "./cmd/google-chat-max")
    decoder = json.JSONDecoder()
    seen = set()
    while raw.strip():
        package, end = decoder.raw_decode(raw.lstrip())
        raw = raw.lstrip()[end:]
        module = package.get("Module")
        if not module or module.get("Main") or module["Path"] in seen:
            continue
        seen.add(module["Path"])
        source = Path(module["Dir"])
        notices = [p for p in source.rglob("*") if
                   p.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "NOTICE")) and p.is_file()]
        if not notices:
            raise SystemExit(f"No license notice found for {module['Path']}@{module['Version']}")
        target = licenses / (module["Path"] + "@" + module["Version"])
        for notice in notices:
            dest = target / notice.relative_to(source)
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(notice, dest)
    shutil.copy2("internal/emojidata/LICENSE", licenses / "UNICODE-LICENSE")
    shutil.copy2(Path(output("go", "env", "GOROOT")) / "LICENSE", licenses / "GO-LICENSE")
    unicode_dir = destination / "internal" / "emojidata"
    unicode_dir.mkdir(parents=True)
    shutil.copy2("internal/emojidata/LICENSE", unicode_dir / "LICENSE")

    revision = output("git", "rev-parse", "--verify", "HEAD")
    if output("git", "status", "--porcelain"):
        revision += " (working tree has changes)"
    executable = args.executable.resolve()
    details = output("go", "version", "-m", executable.name, cwd=executable.parent)
    built_at = output("date", "-u", "+%Y-%m-%dT%H:%M:%SZ")
    (destination / "BUILD.txt").write_text(
        f"Google Chat Max {args.version}\n{args.platform} build\n"
        f"Source commit: {revision}\nBuild time (UTC): {built_at}\n"
        f"Build: CGO_ENABLED={os.environ.get('CGO_ENABLED', '1')} GOMAXPROCS=6 "
        "go build -mod=readonly -tags=netcgo -trimpath -buildvcs=false -p 6 -ldflags=\"-s -w\"\n"
        f"{details}\n"
    )


if __name__ == "__main__":
    main()
