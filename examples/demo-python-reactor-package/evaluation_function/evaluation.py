invocation_count = 0


def evaluation_function(response, answer, params=None):
    global invocation_count
    invocation_count += 1
    if params is None:
        params = {}

    is_correct = str(response).strip() == str(answer).strip()
    feedback_key = (
        "correct_response_feedback"
        if is_correct
        else "incorrect_response_feedback"
    )
    fallback = "reactor package correct" if is_correct else "reactor package incorrect"

    return {
        "is_correct": is_correct,
        "feedback": params.get(feedback_key, fallback),
        "python_reactor_runtime": True,
        "package_mode": True,
        "guest_invocation_count": invocation_count,
        "snapshot_isolation_ok": invocation_count == 1,
    }
