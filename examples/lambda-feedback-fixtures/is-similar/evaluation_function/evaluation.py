from numpy import spacing


def evaluation_function(response, answer, params) -> dict:
    rtol = params.get("rtol", 0)
    atol = params.get("atol", 0)

    if not (isinstance(answer, int) or isinstance(answer, float)):
        raise Exception("Answer must be a number.")

    allowed_diff = atol + rtol * abs(answer) + spacing(answer)
    real_diff = None
    is_correct = False
    feedback = ""
    if not (isinstance(response, int) or isinstance(response, float)):
        feedback = "Please enter a number."
    else:
        real_diff = abs(response - answer)
        allowed_diff = atol + rtol * abs(answer) + spacing(answer)
        is_correct = bool(real_diff <= allowed_diff)

    return {
        "is_correct": is_correct,
        "real_diff": real_diff,
        "allowed_diff": allowed_diff,
        "feedback": feedback,
    }
