"""
Optional: wrap lambda-probe binary in a Python Lambda handler.

Deploy by:
  1. Build binary:  make probe-build
  2. Package both files in a zip with the binary named "lambda-probe":
       zip probe-py.zip tools/lambda-probe/handler.py bin/lambda-probe
  3. Create/update Lambda:
       Runtime: python3.12
       Handler: handler.lambda_handler
       Timeout: 15s

The handler runs the binary, captures its stdout/stderr, and returns JSON.
"""
import json
import os
import subprocess
import stat


SRC_BINARY = "/var/task/lambda-probe"
BINARY = "/tmp/lambda-probe"


def lambda_handler(event, context):
    # /var/task is read-only; copy binary to /tmp and make it executable
    import shutil
    try:
        shutil.copy2(SRC_BINARY, BINARY)
        os.chmod(BINARY, 0o755)
    except Exception as e:
        return {"error": f"copy/chmod failed: {e}"}

    try:
        proc = subprocess.run(
            [BINARY],
            capture_output=True,
            text=True,
            timeout=10,
        )
    except subprocess.TimeoutExpired:
        return {"error": "probe timed out"}
    except Exception as e:
        return {"error": str(e)}

    lines = [l for l in proc.stdout.splitlines() if l.strip() and not l.startswith("#")]
    probe_results = []
    summary = None
    for line in lines:
        try:
            obj = json.loads(line)
            if obj.get("probe") == "summary":
                summary = obj
            else:
                probe_results.append(obj)
        except json.JSONDecodeError:
            pass

    return {
        "probes": probe_results,
        "summary": summary,
        "stderr": proc.stderr[:2000] if proc.stderr else "",
        "exit_code": proc.returncode,
    }
