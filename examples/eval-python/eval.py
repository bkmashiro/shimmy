"""
Example Python evaluation function for Shimmy-WASM (CPython-WASI resident backend).

Contract
--------
evaluation_function(response, answer, params) -> dict
    Called for "eval" requests.
    Must return a dict with at least:
        is_correct  bool
        feedback    str

preview_function(response, answer, params) -> dict  [optional]
    Called for "preview" requests.
    If not defined, the runner falls back to evaluation_function.
    Should return a preview/hint without revealing the full solution.

Shimmy's configured lifecycle must prevent global-variable mutations from
leaking between requests. The invocation counter below is runtime-lane reset
evidence, not application state.
"""


_guest_invocation_count = 0


def evaluation_function(response, answer, params=None):
    """Numeric equality check with configurable absolute tolerance."""
    global _guest_invocation_count
    _guest_invocation_count += 1
    params = params or {}
    tolerance = float(params.get("tolerance", 1e-9))

    try:
        r = float(response)
        a = float(answer)
    except (TypeError, ValueError) as e:
        return {
            "is_correct": False,
            "feedback": f"Error: Could not parse values as numbers: {e}",
            "guest_invocation_count": _guest_invocation_count,
        }

    abs_err = abs(r - a)
    is_correct = abs_err <= tolerance

    if is_correct:
        return {
            "is_correct": True,
            "feedback": f"Correct! {r} matches {a} within tolerance {tolerance}.",
            "absolute_error": abs_err,
            "guest_invocation_count": _guest_invocation_count,
        }
    else:
        return {
            "is_correct": False,
            "feedback": (
                f"Incorrect. Got {r}, expected {a}. "
                f"Absolute error: {abs_err:.6e} (tolerance: {tolerance:.6e})."
            ),
            "absolute_error": abs_err,
            "guest_invocation_count": _guest_invocation_count,
        }


def preview_function(response, answer, params=None):
    """Return a hint without revealing whether the answer is correct."""
    params = params or {}
    tolerance = float(params.get("tolerance", 1e-9))

    try:
        r = float(response)
    except (TypeError, ValueError):
        return {
            "preview": f"Could not parse response '{response}' as a number.",
        }

    return {
        "preview": (
            f"Your answer is {r}. "
            f"The checker uses absolute tolerance {tolerance:.2e}."
        ),
    }
