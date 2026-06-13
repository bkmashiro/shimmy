from sympy.parsing.sympy_parser import T as parser_transformations
from sympy import Equality, latex, pi, Symbol

from .expression_utilities import (
    substitute_input_symbols,
    parse_expression,
    create_sympy_parsing_params,
    create_expression_set,
    convert_absolute_notation
)
from .evaluation_response_utilities import EvaluationResponse
from .feedback.symbolic_equal import internal as symbolic_equal_internal_messages
from .preview import preview_function


def evaluation_function(response, answer, params, include_test_data=False) -> dict:
    """
    Function used to symbolically compare two expressions.
    """

    eval_response = EvaluationResponse()
    eval_response.is_correct = False

    # This code handles the plus_minus and minus_plus operators
    # actual symbolic comparison is done in check_equality
    if "multiple_answers_criteria" not in params.keys():
        params.update({"multiple_answers_criteria": "all"})

    if params.get("is_latex",False):
        response = preview_function(response, params)["preview"]["sympy"]

    response_list = create_expression_set(response, params)
    answer_list = create_expression_set(answer, params)

    if len(response_list) == 1 and len(answer_list) == 1:
        eval_response = check_equality(response, answer, params, eval_response)
    else:
        matches = {"responses": [False]*len(response_list), "answers": [False]*len(answer_list)}
        interp = []
        for i, response in enumerate(response_list):
            result = None
            for j, answer in enumerate(answer_list):
                result = check_equality(response, answer, params, eval_response)
                if result["is_correct"]:
                    matches["responses"][i] = True
                    matches["answers"][j] = True
            if len(interp) == 0:
                interp = result["response_latex"]
                interp_sympy = result["response_simplified"]
            else:
                interp += result["response_latex"]
                interp_sympy += ", " + result["response_simplified"]
        if params["multiple_answers_criteria"] == "all":
            is_correct = all(matches["responses"]) and all(matches["answers"])
            if is_correct is False:
                eval_response.add_feedback(symbolic_equal_internal_messages["MULTIPLE_ANSWER_FAIL_ALL"])
        elif params["multiple_answers_criteria"] == "all_responses":
            is_correct = all(matches["responses"])
            if is_correct is False:
                eval_response.add_feedback(symbolic_equal_internal_messages["MULTIPLE_ANSWER_FAIL_RESPONSE"])
        elif params["multiple_answers_criteria"] == "all_answers":
            is_correct = all(matches["answers"])
            if is_correct is False:
                eval_response.add_feedback(symbolic_equal_internal_messages["MULTIPLE_ANSWER_FAIL_ANSWERS"])
        else:
            raise SyntaxWarning(f"Unknown multiple_answers_criteria: {params['multiple_answers_critera']}")
        eval_response.is_correct = is_correct
        if len(interp) > 1:
            response_latex = "\\left\\{"+",".join(interp)+"\\right\\}"
        else:
            response_latex = interp
        eval_response.latex = response_latex

    return eval_response.serialise(include_test_data)


def check_equality(response, answer, params, eval_response) -> dict:

    if not isinstance(answer, str):
        raise Exception("No answer was given.")
    if not isinstance(response, str):
        eval_response.is_correct = False
        eval_response.add_feedback(("NO_RESPONSE", symbolic_equal_internal_messages["NO_RESPONSE"]))
        return eval_response

    answer = answer.strip()
    response = response.strip()
    if len(answer) == 0:
        raise Exception("No answer was given.")
    if len(response) == 0:
        eval_response.is_correct = False
        eval_response.add_feedback(("NO_RESPONSE", symbolic_equal_internal_messages["NO_RESPONSE"]))
        return eval_response

    answer, response = substitute_input_symbols([answer, response], params)
    parsing_params = create_sympy_parsing_params(params)
    parsing_params.update({"rationalise": True, "simplify": True})
    parsing_params["extra_transformations"] = parser_transformations[9]  # Add conversion of equal signs

    # Converting absolute value notation to a form that SymPy accepts
    response, response_feedback = convert_absolute_notation(response, "response")
    if response_feedback is not None:
        eval_response.add_feedback(response_feedback)
    answer, answer_feedback = convert_absolute_notation(answer, "answer")
    if answer_feedback is not None:
        raise SyntaxWarning(answer_feedback[1], answer_feedback[0])

    if params.get("strict_syntax", True):
        if "^" in response:
            eval_response.add_feedback(("NOTATION_WARNING_EXPONENT", symbolic_equal_internal_messages["NOTATION_WARNING_EXPONENT"]))
        if "!" in response:
            eval_response.add_feedback(("NOTATION_WARNING_FACTORIAL", symbolic_equal_internal_messages["NOTATION_WARNING_FACTORIAL"]))

    # Safely try to parse answer and response into symbolic expressions
    try:
        res = parse_expression(response, parsing_params)
    except Exception:
        eval_response.is_correct = False
        eval_response.add_feedback(("PARSE_ERROR", symbolic_equal_internal_messages["PARSE_ERROR"](response)))
        return eval_response

    try:
        ans = parse_expression(answer, parsing_params)
    except Exception as e:
        raise Exception("SymPy was unable to parse the answer.") from e

    # Add how res was interpreted to the response
    eval_response.latex = latex(res)
    eval_response.simplified = str(res)

    if (not isinstance(res, Equality)) and isinstance(ans, Equality):
        eval_response.is_correct = False
        tag = "EXPRESSION_NOT_EQUALITY"
        eval_response.add_feedback((tag, symbolic_equal_internal_messages[tag]))
        return eval_response

    if isinstance(res, Equality) and (not isinstance(ans, Equality)):
        eval_response.is_correct = False
        tag = "EQUALITY_NOT_EXPRESSION"
        eval_response.add_feedback((tag, symbolic_equal_internal_messages[tag]))
        return eval_response

    # TODO: Remove when criteria for checking proportionality is implemented
    if isinstance(res, Equality) and isinstance(ans, Equality):
        eval_response.is_correct = ((res.args[0]-res.args[1])/(ans.args[0]-ans.args[1])).simplify().is_constant()
        return eval_response

    is_correct = bool((res - ans).simplify() == 0)
    eval_response.is_correct = is_correct

    error_below_atol = None
    error_below_rtol = None

    if eval_response.is_correct is False:
        if params.get("numerical", False) or float(params.get("rtol", 0)) > 0 or float(params.get("atol", 0)) > 0:
            # REMARK: 'pi' should be a reserved symbol but it is sometimes not treated as one, possibly because of input symbols.
            # The two lines below this comments fixes the issue but a more robust solution should be found for cases where there
            # are other reserved symbols.
            def replace_pi(expr):
                pi_symbol = pi
                for s in expr.free_symbols:
                    if str(s) == 'pi':
                        pi_symbol = s
                return expr.subs(pi_symbol, float(pi))
            ans = replace_pi(ans)
            res = replace_pi(res)
            if float(params.get("atol", 0)) > 0:
                try:
                    absolute_error = abs(float(ans-res))
                    error_below_atol = bool(absolute_error < float(params["atol"]))
                except TypeError:
                    error_below_atol = None
            else:
                error_below_atol = True
            if float(params.get("rtol", 0)) > 0:
                try:
                    relative_error = abs(float((ans-res)/ans))
                    error_below_rtol = bool(relative_error < float(params["rtol"]))
                except TypeError:
                    error_below_rtol = None
            else:
                error_below_rtol = True
            if error_below_atol is None or error_below_rtol is None:
                eval_response.is_correct = False
                tag = "NOT_NUMERICAL"
                eval_response.add_feedback((tag, symbolic_equal_internal_messages[tag]))
            elif error_below_atol is True and error_below_rtol is True:
                eval_response.is_correct = True
                tag = "WITHIN_TOLERANCE"
                eval_response.add_feedback((tag, symbolic_equal_internal_messages[tag]))

    return eval_response
