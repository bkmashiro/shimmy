"""Linux E2E fixture shaped like a Lambda Feedback evaluator.

The evaluator owns its public functions and the thin dispatch adapter. Shimmy
only passes the command and validated request payload.
"""

_invocation_count = 0


def evaluation_function(response, answer, params):
    global _invocation_count
    _invocation_count += 1
    tolerance = float((params or {}).get("tolerance", 0.0))
    actual = float(response)
    expected = float(answer)
    is_correct = abs(actual - expected) <= tolerance
    return {
        "is_correct": is_correct,
        "feedback": "correct" if is_correct else "incorrect",
        "invocation_count": _invocation_count,
    }


def preview_function(response, params):
    global _invocation_count
    _invocation_count += 1
    return {
        "preview": f"submitted: {response}",
        "invocation_count": _invocation_count,
    }


def dispatch(method, payload):
    if method == "eval":
        return evaluation_function(
            payload.get("response"),
            payload.get("answer"),
            payload.get("params", {}),
        )
    if method == "preview":
        return preview_function(
            payload.get("response"),
            payload.get("params", {}),
        )
    raise LookupError("unsupported method: " + str(method))
