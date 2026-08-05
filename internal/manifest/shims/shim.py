#!/usr/bin/env python3
"""descendence job param shim (task 6.4) - Python.

argv is [this shim, the real script's path] - manifest.Manifest.Argv puts
it there when the job declares at least one param, names no explicit
command, and the script's own extension is .py. Reads params.json - an
array of {"name":..., "value":...} in contract order (task 6.2/6.3) - and
re-execs the real script with one "--name value" flag per param, so a
normal argparse-based script consumes them exactly as if invoked directly.
"""
import json
import os
import sys


def main():
    script = sys.argv[1]
    params_path = os.environ.get("DESCENDENCE_PARAMS_FILE", "/run/job/params.json")
    with open(params_path) as f:
        params = json.load(f)

    args = [script]
    for p in params:
        value = p["value"]
        if isinstance(value, bool):
            value = "true" if value else "false"
        args.append(f"--{p['name']}")
        args.append(str(value))

    os.execvp(script, args)


if __name__ == "__main__":
    main()
