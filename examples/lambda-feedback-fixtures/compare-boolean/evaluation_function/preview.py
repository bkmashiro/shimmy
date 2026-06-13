from typing import Any
from lf_toolkit.preview import Result, Params, Preview

from .parse import parse_with_feedback, FeedbackException
from .evaluation import get_disallowed

def preview_function(response: Any, params: Params) -> Result:
    """
    Function used to preview a student response.
    ---
    The handler function passes three arguments to preview_function():

    - `response` which are the answers provided by the student.
    - `params` which are any extra parameters that may be useful,
        e.g., error tolerances.

    The output of this function is what is returned as the API response
    and therefore must be JSON-encodable. It must also conform to the
    response schema.

    Any standard python library may be used, as well as any package
    available on pip (provided it is added to requirements.txt).

    The way you wish to structure you code (all in this function, or
    split into many) is entirely up to you.
    """

    disallowed = get_disallowed([])

    try:
        result, _ = parse_with_feedback(response, disallowed, latex=params.get("is_latex", False))

        latex = result.to_latex()
        ascii = str(result)

        return Result(preview=Preview(latex=latex,sympy=ascii))
    except FeedbackException as e:
        return Result(preview=Preview(feedback=str(e)))
    except Exception as e:
        return Result(preview=Preview(feedback=str(e)))
