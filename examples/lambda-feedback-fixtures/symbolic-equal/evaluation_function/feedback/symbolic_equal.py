# TODO: Handle multiple answer feedback properly
internal = {
    "ABSOLUTE_VALUE_NOTATION_AMBIGUITY": lambda name: f"Notation in {name} might be ambiguous, use `Abs(.)` instead of `|.|`",
    "NO_RESPONSE": "No response submitted.",
    "MULTIPLE_ANSWER_FAIL_ALL": "At least one answer or response was incorrect.",
    "MULTIPLE_ANSWER_FAIL_RESPONSE": "At least one response was incorrect.",
    "MULTIPLE_ANSWER_FAIL_ANSWERS": "At least one answer is missing in the response.",
    "PARSE_ERROR": lambda x: f"`{x}` could not be parsed as a valid mathematical expression. Ensure that correct codes for input symbols are used, correct notation is used, that the expression is unambiguous and that all parentheses are closed.",
    "NOTATION_WARNING_EXPONENT": "Note that `^` cannot be used to denote exponentiation, use `**` instead.",
    "NOTATION_WARNING_FACTORIAL": "Note that `!` cannot be used to denote factorial, use `factorial(...)` instead.",
    "EXPRESSION_NOT_EQUALITY": "The response was an expression but was expected to be an equality.",
    "EQUALITY_NOT_EXPRESSION": "The response was an equality but was expected to be an expression.",
    "WITHIN_TOLERANCE": "",  # "The difference between the response the answer is within specified error tolerance.",
    "NOT_NUMERICAL": "", #"The expression cannot be evaluated numerically.",
}
