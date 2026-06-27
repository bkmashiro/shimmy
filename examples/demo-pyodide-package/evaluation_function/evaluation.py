invocation_count = 0


def evaluation_function(response, answer, params):
    global invocation_count
    invocation_count += 1

    is_correct = str(response).strip() == str(answer).strip()
    feedback_key = (
        "correct_response_feedback"
        if is_correct
        else "incorrect_response_feedback"
    )
    fallback = "package correct" if is_correct else "package incorrect"

    return {
        "is_correct": is_correct,
        "feedback": params.get(feedback_key, fallback),
        "pyodide_runtime": True,
        "package_mode": True,
        "guest_invocation_count": invocation_count,
        "fresh_namespace_ok": invocation_count == 1,
    }
