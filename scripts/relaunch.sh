#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
exec python3 - "$PWD/bin/google-chat-max" <<'PY'
import os, signal, sys, time
from pathlib import Path
exe=Path(sys.argv[1]).resolve()
pids=[]
for process in Path('/proc').iterdir():
    if not process.name.isdigit(): continue
    try:
        target=os.readlink(process/'exe').removesuffix(' (deleted)')
        if target==str(exe):
            pid=int(process.name)
            os.kill(pid,signal.SIGTERM)
            pids.append(pid)
    except (OSError,ValueError): pass
deadline=time.monotonic()+150
while any(Path('/proc',str(pid)).exists() for pid in pids):
    if time.monotonic()>deadline:
        raise SystemExit('Existing app is still cleaning up; it was not force-killed.')
    time.sleep(.1)
os.execv(str(exe),[str(exe)])
PY
