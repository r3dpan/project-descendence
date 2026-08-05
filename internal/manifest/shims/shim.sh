#!/bin/bash
# descendence job param shim (task 6.4) - Bash.
#
# argv is [this shim, the real script's path] - manifest.Manifest.Argv puts
# it there when the job declares at least one param, names no explicit
# command, and the script's own extension is .sh. Reads params.argv - one
# NUL-terminated positional value per param, in contract order (task 6.3,
# manifest.ParamsArgv) - and re-execs the real script with them appended as
# $1, $2, .... The script sees plain positional arguments and has no notion
# a shim ran.
#
# NUL-delimited rather than parsing params.json directly: it sidesteps
# writing a JSON parser in Bash entirely, and it is exact for any byte a
# param value can hold (quotes, newlines, anything but a literal NUL) with
# no escaping at all.
set -euo pipefail

script="$1"
argv_file="${DESCENDENCE_PARAMS_ARGV_FILE:-/run/job/params.argv}"

args=()
if [ -s "$argv_file" ]; then
    mapfile -d '' -t args <"$argv_file"
fi

exec "$script" "${args[@]}"
