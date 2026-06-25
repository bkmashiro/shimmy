"""Small Pyodide-backed Python evaluator example for Shimmy.

The module is intentionally pure Python so the demo is fast, but it runs in
Pyodide's CPython WebAssembly runtime inside Node.js. It also keeps a global
counter so the example can demonstrate that legacy script mode executes the
source in a fresh namespace for each request.
"""

invocation_count = 0


def evaluation_function(response, answer, params=None):
    global invocation_count
    invocation_count += 1

    params = params or {}
    correct_feedback = params.get(
        "correct_response_feedback",
        "Correct — Pyodide executed this Python evaluator.",
    )
    incorrect_feedback = params.get(
        "incorrect_response_feedback",
        "Incorrect — Pyodide executed this Python evaluator.",
    )

    is_correct = str(response).strip() == str(answer).strip()
    return {
        "is_correct": is_correct,
        "feedback": correct_feedback if is_correct else incorrect_feedback,
        "pyodide_runtime": True,
        "guest_invocation_count": invocation_count,
        "fresh_namespace_ok": invocation_count == 1,
    }
