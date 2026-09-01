import numpy as np
from evaluation_function_utils.errors import EvaluationException


def evaluation_function(response, answer, params) -> dict:
    try:
        process_element(answer)
    except ValueError:
        raise Exception("Answer has at least one field that is not a number.")
    except Exception:
        raise Exception("Answer has empty fields.")

    feedback = ""
    try:
        process_element(response)
    except ValueError:
        feedback = "Only numbers are permitted."
    except Exception:
        feedback = "Response has at least one empty field."

    if feedback:
        return {"is_correct": False, "feedback": feedback}

    try:
        ans = np.array(answer, dtype=np.float32)
    except ValueError as exc:
        raise EvaluationException("Failed to parse correct answer", detail=repr(exc))

    try:
        res = np.array(response, dtype=np.float32)
    except ValueError as exc:
        raise EvaluationException("Failed to parse response", detail=repr(exc))

    rtol = params.get("rtol", 0)
    atol = params.get("atol", 0)
    is_correct = np.allclose(res, ans, rtol=rtol, atol=atol)

    if is_correct is False and params.get("feedback_for_incorrect_response") is not None:
        return {
            "is_correct": is_correct,
            "feedback": params["feedback_for_incorrect_response"],
        }

    return {"is_correct": is_correct}


def process_element(element):
    if isinstance(element, list):
        for child in element:
            process_element(child)
        return
    if isinstance(element, str):
        element = element.strip()
        if len(element) == 0 or "element" == "undefined":
            raise Exception("Contains an empty element")
        float(element)
