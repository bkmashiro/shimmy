import sys
import json


def evaluation_function(response, answer, params=None):
    """Simple numeric comparison eval function."""
    try:
        is_correct = abs(float(response) - float(answer)) < 1e-9
        return {
            "is_correct": is_correct,
            "feedback": f"{'Correct' if is_correct else 'Incorrect'}: got {response}, expected {answer}"
        }
    except (TypeError, ValueError) as e:
        return {"is_correct": False, "feedback": f"Error: {e}"}


if __name__ == "__main__":
    request = json.loads(sys.stdin.read())
    result = evaluation_function(
        request.get("response"),
        request.get("answer"),
        request.get("params", {})
    )
    print(json.dumps({"is_correct": result["is_correct"], "feedback": result["feedback"]}))
