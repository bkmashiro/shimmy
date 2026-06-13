from typing import Any
from sympy import simplify_logic, Equivalent
from lf_toolkit.evaluation import Result, Params

from .parse import parse_with_feedback, FeedbackException

def get_disallowed(disallowed_list: list[str]) -> dict:
    disallowed = {}
    for op in ["and", "or", "not", "xor"]:
        disallowed.update({op: op in disallowed_list})
    return disallowed

def evaluation_function(
    response: Any,
    answer: Any,
    params: Params,
) -> Result:
    """
    Function used to evaluate a student response.
    ---
    The handler function passes three arguments to evaluation_function():

    - `response` which are the answers provided by the student.
    - `answer` which are the correct answers to compare against.
    - `params` which are any extra parameters that may be useful,
        e.g., error tolerances.

    The output of this function is what is returned as the API response
    and therefore must be JSON-encodable. It must also conform to the
    response schema.

    Any standard python library may be used, as well as any package
    available on pip (provided it is added to requirements.txt).

    The way you wish to structure you code (all in this function, or
    split into many) is entirely up to you. All that matters are the
    return types and that evaluation_function() is the main function used
    to output the evaluation response.
    """

    # here we want to compare the response set with the example solution set.
    # we have to do the following steps

    # create a dictionary of which operations are allowed:
    disallowed = get_disallowed(params.get("disallowed", []))

    try:
        # 1. convert the `response`, which may be a latex string, to a sympy expression
        response_set, response_set_sympy = parse_with_feedback(response, disallowed, latex=params.get("is_latex", False))

        # 2. convert the `answer`, which may be a latex string, to a sympy expression
        # TODO: what if answer is also in latex? how do we know?
        _, answer_set_sympy = parse_with_feedback(answer, disallowed, latex=False)

        # 3. compare the two sympy expressions w/ simplification enabled.
        #    If they are equal, the sets produced by the two expressions are
        #    semantically equal. However, the expressions may not be equal.
        semantic_equal = simplify_logic(Equivalent(response_set_sympy, answer_set_sympy)) == True

        # 4. compare the two sympy expressions w/ simplifaction disabled.
        #    If they are equal, the expressions are also equal in syntax.
        #    This respects laws of commutativity, e.g. A u B == B u A.
        syntactic_equal = response_set_sympy == answer_set_sympy

        enforce_expression_equality = params.get("enforce_expression_equality", False)

        # 5. `is_correct` is True, iff 3) is True, and either 4) or `enforce_expression_equality` is True
        is_correct = semantic_equal and (syntactic_equal or not enforce_expression_equality)

        feedback_items=[]

        if semantic_equal and not syntactic_equal and enforce_expression_equality:
            feedback_items.append(("syntactic_equality", "The expressions are not equal syntacitcally."))
        elif not semantic_equal:
            feedback_items.append(("semantic_equality", "The expressions are not equal."))

        latex = response_set.to_latex()

        ascii = str(response_set)

        return Result(
            is_correct=is_correct,
            latex=latex,
            simplified=ascii,
            feedback_items=feedback_items,
        )
    except FeedbackException as e:
        return Result(
            is_correct=False,
            feedback_items=[("parse_error", str(e))]
        )