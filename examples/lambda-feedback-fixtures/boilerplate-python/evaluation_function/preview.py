from typing import Any

from lf_toolkit.preview import Params, Preview, Result


def preview_function(response: Any, params: Params) -> Result:
    return Result(preview=Preview(sympy=response))
