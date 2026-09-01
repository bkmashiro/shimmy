from typing import Any

from lf_toolkit.evaluation import Params, Result


def evaluation_function(
    response: Any,
    answer: Any,
    params: Params,
) -> Result:
    return Result(is_correct=response == answer)
