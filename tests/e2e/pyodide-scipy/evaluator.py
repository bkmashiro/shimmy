from scipy.special import expit


def evaluation_function(response, answer, params):
    value = float(expit(float(response)))
    tolerance = float(params.get("tolerance", 1e-12))
    return {
        "is_correct": abs(value - float(answer)) <= tolerance,
        "value": value,
    }


def preview_function(response, params):
    return {"preview": {"sigmoid": float(expit(float(response)))}}
