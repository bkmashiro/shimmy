"""
Example Python eval function using scipy — for use with the Pyodide runner.

This demonstrates a statistical evaluation that would fail under CPython-WASI
due to LAPACK/f2c symbol issues in scipy's Fortran-based linear algebra.
The Pyodide runner side-steps this by running Python inside Node.js via
Pyodide's WebAssembly build of scipy.

Function contract (must be named evaluation_function):
    evaluation_function(response, answer, params=None) -> dict

    response: student answer (str or numeric)
    answer:   reference answer (str or numeric)
    params:   optional dict of additional parameters
              supported keys:
                "tolerance"  float, absolute tolerance for numeric checks
                             (default 1e-6)
                "test"       "ttest" | "numeric" (default "numeric")

Returns dict with at least:
    is_correct  bool
    feedback    str
    (plus optional extra fields from the chosen test)
"""

from scipy import stats
import math


def evaluation_function(response, answer, params=None):
    """
    Evaluate a student numeric response against a reference answer.

    Supports two modes (controlled by params["test"]):

    - "numeric" (default): exact numeric comparison with tolerance
    - "ttest": one-sample t-test to check whether the student's reported mean
               is consistent with the reference value (params must supply a
               list of samples via params["samples"])
    """
    if params is None:
        params = {}

    test_mode = params.get("test", "numeric")

    try:
        if test_mode == "ttest":
            return _ttest_evaluation(response, answer, params)
        else:
            return _numeric_evaluation(response, answer, params)

    except Exception as exc:
        return {
            "is_correct": False,
            "feedback": f"Evaluation error: {exc}",
        }


def _numeric_evaluation(response, answer, params):
    """Strict numeric comparison with configurable absolute tolerance."""
    tolerance = float(params.get("tolerance", 1e-6))

    try:
        r = float(response)
        a = float(answer)
    except (TypeError, ValueError) as e:
        return {
            "is_correct": False,
            "feedback": f"Could not parse values as numbers: {e}",
        }

    is_correct = math.isclose(r, a, abs_tol=tolerance, rel_tol=0.0)

    if is_correct:
        feedback = f"Correct! Your answer {r} matches the expected value {a}."
    else:
        error = abs(r - a)
        feedback = (
            f"Incorrect. Got {r}, expected {a}. "
            f"Absolute error: {error:.6g} (tolerance: {tolerance:.6g})."
        )

    return {
        "is_correct": is_correct,
        "feedback": feedback,
        "absolute_error": abs(r - a),
    }


def _ttest_evaluation(response, answer, params):
    """
    One-sample t-test evaluation.

    Checks whether 'samples' in params is statistically consistent with the
    reference value 'answer' at alpha=0.05.

    'response' is ignored in this mode — the evaluation is purely on the
    samples the student provides.
    """
    alpha = float(params.get("alpha", 0.05))
    samples = params.get("samples")

    if samples is None:
        return {
            "is_correct": False,
            "feedback": "t-test mode requires params['samples'] (list of numbers).",
        }

    try:
        samples = [float(s) for s in samples]
        popmean = float(answer)
    except (TypeError, ValueError) as e:
        return {
            "is_correct": False,
            "feedback": f"Invalid samples or answer: {e}",
        }

    if len(samples) < 2:
        return {
            "is_correct": False,
            "feedback": "t-test requires at least 2 samples.",
        }

    t_stat, p_value = stats.ttest_1samp(samples, popmean)

    # Fail to reject H0 (p > alpha) means the data is consistent with popmean.
    is_correct = p_value > alpha

    sample_mean = sum(samples) / len(samples)

    if is_correct:
        feedback = (
            f"Correct. Sample mean {sample_mean:.4g} is not significantly "
            f"different from {popmean} (p={p_value:.4g} > α={alpha})."
        )
    else:
        feedback = (
            f"Incorrect. Sample mean {sample_mean:.4g} differs significantly "
            f"from {popmean} (p={p_value:.4g} ≤ α={alpha})."
        )

    return {
        "is_correct": is_correct,
        "feedback": feedback,
        "t_statistic": t_stat,
        "p_value": p_value,
        "sample_mean": sample_mean,
        "n_samples": len(samples),
    }
