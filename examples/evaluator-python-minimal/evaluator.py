"""Minimal Lambda Feedback-style evaluator for Shimmy examples.

Evaluator authors only need to provide plain Python functions. Shimmy's Python
compatibility workers load them by module:function name and normalize the return
value to the HTTP response schema.
"""

from __future__ import annotations

from typing import Any


def evaluation_function(response: Any, answer: Any, params: dict[str, Any] | None = None) -> dict[str, Any]:
    """Grade one response.

    Keep evaluator state in local variables. Some runtimes restore or restart
    execution state between requests, so module-level mutable state should not be
    used for correctness.
    """

    normalized_response = str(response).strip()
    normalized_answer = str(answer).strip()
    is_correct = normalized_response == normalized_answer
    return {
        "is_correct": is_correct,
        "feedback": "Correct." if is_correct else f"Expected {normalized_answer!r}, got {normalized_response!r}.",
    }


def preview_function(response: Any, answer: Any = None, params: dict[str, Any] | None = None) -> dict[str, Any]:
    """Preview a submitted response before grading.

    The optional ``answer`` argument keeps this compatible with the common
    ``preview(response, answer=None, params=None)`` shape. Shimmy's adapter also
    accepts two-argument preview helpers such as ``preview(response, params)``.
    """

    del answer
    params = params or {}
    prefix = str(params.get("prefix", "Preview"))
    return {"preview": f"{prefix}: {str(response).strip()}"}
