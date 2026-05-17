#!/usr/bin/env bash
# build-runner.sh — embed an eval.js into runner.js and compile with javy.
#
# Usage:
#   ./build-runner.sh [eval.js] [output.wasm]
#
# Defaults:
#   eval.js     → ./eval.js
#   output.wasm → ./runner.wasm
#
# Requirements:
#   javy  — https://github.com/bytecodealliance/javy/releases
#           Must be on PATH or set JAVY=/path/to/javy
#   node or python3 — for JSON-encoding the eval source
#
# The script:
#   1. Reads the eval JS source from <eval.js>
#   2. JSON-encodes it and patches the EVAL_SOURCE constant in runner.js (in a temp file)
#   3. Compiles the patched runner with javy → <output.wasm>

set -euo pipefail

EVAL_JS="${1:-eval.js}"
OUTPUT_WASM="${2:-runner.wasm}"
RUNNER_JS="$(dirname "$(readlink -f "$0")")/runner.js"
JAVY="${JAVY:-javy}"

# Verify prerequisites
if ! command -v "$JAVY" &>/dev/null; then
    echo "Error: javy not found on PATH. Set JAVY=/path/to/javy or install from:" >&2
    echo "  https://github.com/bytecodealliance/javy/releases" >&2
    exit 1
fi

if [[ ! -f "$EVAL_JS" ]]; then
    echo "Error: eval script not found: $EVAL_JS" >&2
    exit 1
fi

if [[ ! -f "$RUNNER_JS" ]]; then
    echo "Error: runner.js not found: $RUNNER_JS" >&2
    exit 1
fi

# JSON-encode the eval source so it can be embedded as a JS string literal.
# We write the JSON to a temp file to avoid shell variable quoting issues with
# backslash sequences.
JSON_TMP="$(mktemp /tmp/shimmy-eval-json-XXXXXX.txt)"
PATCHED_RUNNER="$(mktemp /tmp/shimmy-runner-XXXXXX.js)"
trap 'rm -f "$JSON_TMP" "$PATCHED_RUNNER"' EXIT

if command -v node &>/dev/null; then
    node -e "
        const fs = require('fs');
        process.stdout.write(JSON.stringify(fs.readFileSync(process.argv[1], 'utf8')));
    " -- "$EVAL_JS" > "$JSON_TMP"
elif command -v python3 &>/dev/null; then
    python3 -c "
import sys, json
sys.stdout.write(json.dumps(open(sys.argv[1]).read()))
" -- "$EVAL_JS" > "$JSON_TMP"
else
    echo "Error: need node or python3 to JSON-encode eval.js source" >&2
    exit 1
fi

# Patch runner.js: replace the EVAL_SOURCE sentinel block with the embedded source.
# The block in runner.js looks like:
#   /*EVAL_SOURCE_BEGIN*/
#   var EVAL_SOURCE = (function () { ... })();
#   /*EVAL_SOURCE_END*/
#
# IMPORTANT: use a lambda in re.sub so that backslash sequences in the JSON
# string (e.g. \n) are not interpreted as regex replacement escapes.
python3 - "$RUNNER_JS" "$JSON_TMP" "$PATCHED_RUNNER" <<'PYEOF'
import sys, re

runner_path = sys.argv[1]
json_path   = sys.argv[2]
output_path = sys.argv[3]

with open(json_path) as f:
    eval_source_json = f.read().strip()

with open(runner_path) as f:
    src = f.read()

replacement = (
    "/*EVAL_SOURCE_BEGIN*/\n"
    "var EVAL_SOURCE = " + eval_source_json + ";\n"
    "/*EVAL_SOURCE_END*/"
)

# Use lambda to prevent re.sub from interpreting \n, \t etc. in replacement.
patched, count = re.subn(
    r'/\*EVAL_SOURCE_BEGIN\*/.*?/\*EVAL_SOURCE_END\*/',
    lambda m: replacement,
    src,
    flags=re.DOTALL,
)

if count == 0:
    print("Error: EVAL_SOURCE sentinel block not found in runner.js", file=sys.stderr)
    sys.exit(1)

with open(output_path, "w") as f:
    f.write(patched)

print(f"Patched runner written to {output_path}", file=sys.stderr)
PYEOF

echo "Compiling with javy: $(basename "$PATCHED_RUNNER") → $OUTPUT_WASM" >&2
"$JAVY" build -J javy-stream-io=y "$PATCHED_RUNNER" -o "$OUTPUT_WASM"

echo "Done: $OUTPUT_WASM" >&2
ls -lh "$OUTPUT_WASM"
