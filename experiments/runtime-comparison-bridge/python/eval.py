"""Shared pure-Python fixture for native CPython, Shimmy Python, and Pyodide."""

MAX_ITERATIONS = 5_000_000
_MASK = (1 << 64) - 1
_MULTIPLIER = 6364136223846793005
_INCREMENT = 1442695040888963407
_guest_invocation_count = 0


def evaluation_function(response, answer, params=None):
    global _guest_invocation_count
    _guest_invocation_count += 1
    params = params or {}
    iterations = params.get("iterations", 0)
    seed = params.get("seed", 0)

    if isinstance(iterations, bool) or not isinstance(iterations, int):
        return _error("iterations must be an integer")
    if iterations < 0 or iterations > MAX_ITERATIONS:
        return _error(f"iterations must be in [0,{MAX_ITERATIONS}]")
    if isinstance(seed, bool) or not isinstance(seed, int) or seed < 0 or seed > _MASK:
        return _error("seed must be an unsigned 64-bit integer")

    value = seed
    for index in range(iterations):
        value = (value * _MULTIPLIER + _INCREMENT + index) & _MASK

    return {
        "is_correct": response == answer,
        "work_checksum": f"{value:016x}",
        "guest_invocation_count": _guest_invocation_count,
    }


def _error(message):
    return {
        "is_correct": False,
        "error": message,
        "guest_invocation_count": _guest_invocation_count,
    }
