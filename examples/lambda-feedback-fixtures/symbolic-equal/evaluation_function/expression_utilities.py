# -------- String Manipulation imports
from .slr_parsing_utilities import (
    SLR_expression_parser,
    infix,
    group,
    compose
)

from .feedback.symbolic_equal import internal as symbolic_equal_internal_messages

# (Sympy) Expression Parsing imports
from sympy.parsing.sympy_parser import parse_expr, split_symbols_custom, _token_splittable
from sympy.parsing.sympy_parser import T as parser_transformations
from sympy.printing.latex import LatexPrinter
from sympy import Basic, Symbol
import re
from typing import Dict, List, TypedDict

class ModifiedLatexPrinter(LatexPrinter):
    """Modified LatexPrinter class that prints logarithms other than the natural logarithm correctly.
    """
    def _print_log(self, expr, exp=None):
        if self._settings["ln_notation"] and len(expr.args) < 2:
            log_not = r"\ln"
        else:
            log_not = r"\log"
        if len(expr.args) > 1:
            base = self._print(expr.args[1])
            log_not = r"\log_{%s}" % base
        tex = r"%s{\left(%s \right)}" % (log_not, self._print(expr.args[0]))

        if exp is not None:
            return r"%s^{%s}" % (tex, exp)
        else:
            return tex

elementary_functions_names = [
    ('sin', []), ('sinc', []), ('csc', ['cosec']), ('cos', []), ('sec', []), ('tan', []), ('cot', ['cotan']),
    ('asin', ['arcsin']), ('acsc', ['arccsc', 'arccosec', 'acosec']), ('acos', ['arccos']), ('asec', ['arcsec']),
    ('atan', ['arctan']), ('acot', ['arccot', 'arccotan', 'acotan']), ('atan2', ['arctan2']),
    ('sinh', []), ('cosh', []), ('tanh', []), ('csch', ['cosech']), ('sech', []),
    ('asinh', ['arcsinh']), ('acosh', ['arccosh']), ('atanh', ['arctanh']),
    ('acsch', ['arccsch', 'arccosech']), ('asech', ['arcsech']),
    ('exp', ['Exp']), ('E', ['e']), ('log', ['ln']),
    ('sqrt', []), ('sign', []), ('Abs', ['abs']), ('Max', ['max']), ('Min', ['min']), ('arg', []), ('ceiling', ['ceil']), ('floor', []),
    # Below this line should probably not be collected with elementary functions. Some like 'common operations' would be a better name
    ('summation', ['sum','Sum']), ('Derivative', ['diff']), 
]
for data in elementary_functions_names:
    upper_case_alternatives = [data[0].upper()]
    for alternative in data[1]:
        if alternative.upper() not in upper_case_alternatives:
            upper_case_alternatives.append(alternative.upper())
    data[1].extend(upper_case_alternatives)

greek_letters = [
    "Alpha", "alpha", "Beta", "beta", "Gamma", "gamma", "Delta", "delta", "Epsilon", "epsilon", "Zeta", "zeta",
    "Eta", "eta", "Theta", "theta", "Iota", "iota", "Kappa", "kappa", "Lambda", # "lambda" removed to avoid collision with reserved keyword in python
    "Mu", "mu", "Nu", "nu",
    "Xi", "xi", "Omicron", "omicron", "Pi", "pi", "Rho", "rho", "Sigma", "sigma", "Tau", "tau", "Upsilon", "upsilon",
    "Phi", "phi", "Chi", "chi", "Psi", "psi", "Omega", "omega"
]
special_symbols_names = [(x, []) for x in greek_letters]


# -------- String Manipulation Utilities
def create_expression_set(expr, params):
    expr_set = set()
    if "plus_minus" in params.keys():
        expr = expr.replace(params["plus_minus"], "plus_minus")

    if "minus_plus" in params.keys():
        expr = expr.replace(params["minus_plus"], "minus_plus")

    if ("plus_minus" in expr) or ("minus_plus" in expr):
        expr_set.add(expr.replace("plus_minus", "+").replace("minus_plus", "-"))
        expr_set.add(expr.replace("plus_minus", "-").replace("minus_plus", "+"))
    else:
        expr_set.add(expr)

    return list(expr_set)


def convert_absolute_notation(expr, name):
    """
    Accept || as another form of writing modulus of an expression.
    Function makes the input parseable by SymPy, SymPy only accepts Abs()
    REMARK: this function cannot handle nested ||. It will attempt to pair
    each | with the closest | to the right and return a string with a warning
    if it detects an ambiguity.

    Parameters
    ----------
    expr : string
        Expression to convert, might have ||

    Returns
    -------
    expr : string
        converted response input
    """

    # positions of the || values
    n_expr = expr.count('|')
    if n_expr == 2:
        expr = list(expr)
        expr[expr.index("|")] = "Abs("
        expr[expr.index("|")] = ")"
        expr = "".join(expr)
    elif n_expr > 0:
        expr_start_abs_pos = []
        expr_end_abs_pos = []
        expr_ambiguous_abs_pos = []

        if expr[0] == "|":
            expr_start_abs_pos.append(0)
        for i in range(1, len(expr)-1):
            if expr[i] == "|":
                if (expr[i-1].isalnum() or expr[i-1] in "()[]{}") and not (expr[i+1].isalnum() or expr[i+1] in "()[]{}"):
                    expr_end_abs_pos.append(i)
                elif (expr[i+1].isalnum() or expr[i+1] in "()[]{}") and not (expr[i-1].isalnum() or expr[i-1] in "()[]{}"):
                    expr_start_abs_pos.append(i)
                else:
                    expr_ambiguous_abs_pos.append(i)
        if expr[-1] == "|":
            expr_end_abs_pos.append(len(expr)-1)
        expr = list(expr)
        for i in expr_start_abs_pos:
            expr[i] = "Abs("
        for i in expr_end_abs_pos:
            expr[i] = ")"
        k = 0
        prev_ambiguous = -1
        for i in expr_ambiguous_abs_pos:
            prev_start = -1
            for j in expr_start_abs_pos:
                if j < i:
                    prev_start = j
                else:
                    break
            prev_end = -1
            for j in expr_end_abs_pos:
                if j < i:
                    prev_end = j
                else:
                    break
            if max(prev_start, prev_end, prev_ambiguous) == prev_end:
                if expr[i-1].isalnum():
                    expr[i] = "*Abs("
                else:
                    expr[i] = "Abs("
            elif max(prev_start, prev_end, prev_ambiguous) == prev_ambiguous:
                if k % 2 == 0:
                    if expr[i-1].isalnum():
                        expr[i] = "*Abs("
                    else:
                        expr[i] = "Abs("
                else:
                    expr[i] = ")"
                k += 1
            else:
                expr[i] = ")"
            prev_ambiguous = i
        expr = "".join(expr)

    ambiguity_tag = "ABSOLUTE_VALUE_NOTATION_AMBIGUITY"
    remark = ""
    if n_expr > 2 and len(expr_ambiguous_abs_pos) > 0:
        remark = symbolic_equal_internal_messages[ambiguity_tag](name)

    feedback = None
    if len(remark) > 0:
        feedback = (ambiguity_tag, remark)

    return expr, feedback


def SLR_implicit_multiplication_convention_parser(convention):
    delimiters = [
        (("(", ")"), group(1))
    ]

    costum_tokens = [
        (" *(\*|\+|-| ) *", "SPLIT"), (" */ *", "SOLIDUS")
    ]

    infix_operators = []
    costum_productions = [("E", "*E", group(2, empty=True)), ("E", "EE", group(2, empty=True))]
    if convention == "equal_precedence":
        costum_productions += [("E", "E/E", infix)]
    elif convention == "implicit_higher_precedence":
        costum_productions += [("E", "E/E", compose(infix, group(1, empty=True, delimiters=["(", ")"])))]
    else:
        raise Exception(f"Unknown convention {convention}")

    undefined = ("O", "OTHER")
    expression_node = ("E", "EXPRESSION_NODE")
    return SLR_expression_parser(delimiters=delimiters, infix_operators=infix_operators, undefined=undefined, expression_node=expression_node, costum_tokens=costum_tokens, costum_productions=costum_productions)


def preprocess_according_to_chosen_convention(expression, parameters):
    convention = parameters.get("convention", None)
    if convention is not None:
        parser = SLR_implicit_multiplication_convention_parser(convention)
        expression = parser.parse(parser.scan(expression))[0].content_string()
    return expression


def substitute_input_symbols(exprs, params):
    '''
    Input:
        exprs  : a string or a list of strings
        params : Evaluation function parameter dictionary
    Output:
        List of strings where alternatives for input symbols have been replaced with
        their corresponsing input symbol code.
    Remark:
        Alternatives are sorted before substitution so that longer alternatives takes precedence.
    '''
    if isinstance(exprs, str):
        exprs = [exprs]

    substitutions = [(expr, expr) for expr in params.get("reserved_keywords",[])]

    input_symbols = params.get("symbols",dict())

    if "symbols" in params.keys():
        # Removing invalid input symbols
        input_symbols_to_remove = []
        aliases_to_remove = []
        for (code, symbol_data) in input_symbols.items():
            if len(code) == 0:
                input_symbols_to_remove += [code]
            else:
                if len(code.strip()) == 0:
                    input_symbols_to_remove += [code]
                else:
                    aliases = symbol_data["aliases"]
                    for i in range(0, len(aliases)):
                        if len(aliases[i]) > 0:
                            aliases[i].strip()
                        if len(aliases[i]) == 0:
                            aliases_to_remove += [(code, i)]
        for (code, i) in aliases_to_remove:
            del input_symbols[code]["aliases"][i]
        for code in input_symbols_to_remove:
            del input_symbols[code]

    # Since 'lambda' is a reserved keyword in python
    # it needs to be replaced with 'lamda' for expression
    # parsing to work properly
    lambda_value = input_symbols.pop("lambda", {"latex": r"\lambda", "aliases": ["lambda"]})
    if lambda_value is not None:
        lambda_value["aliases"].append("lambda")
    input_symbols.update({"lamda": lambda_value})
    params.update({"symbols": input_symbols})

    for (code, symbol_data) in input_symbols.items():
        substitutions.append((code, code))
        for alias in symbol_data["aliases"]:
            if len(alias) > 0:
                substitutions.append((alias, code))

    # REMARK: This is to ensure capability with response areas that use the old formatting
    # for input_symbols. Should be removed when all response areas are updated.
    if "input_symbols" in params.keys():
        input_symbols = params["input_symbols"]
        input_symbols_to_remove = []
        alternatives_to_remove = []
        for k in range(0, len(input_symbols)):
            if len(input_symbols[k]) > 0:
                input_symbols[k][0].strip()
                if len(input_symbols[k][0]) == 0:
                    input_symbols_to_remove += [k]
            else:
                for i in range(0, len(input_symbols[k][1])):
                    if len(input_symbols[k][1][i]) > 0:
                        input_symbols[k][1][i].strip()
                    if len(input_symbols[k][1][i]) == 0:
                        alternatives_to_remove += [(k, i)]
        for (k, i) in alternatives_to_remove:
            del input_symbols[k][1][i]
        for k in input_symbols_to_remove:
            del input_symbols[k]
        for input_symbol in params["input_symbols"]:
            substitutions.append((input_symbol[0], input_symbol[0]))
            for alternative in input_symbol[1]:
                if len(alternative) > 0:
                    substitutions.append((alternative, input_symbol[0]))

    if len(substitutions) > 0:
        substitutions.sort(key=lambda x: -len(x[0]))
        for k in range(0, len(exprs)):
            exprs[k] = substitute(exprs[k], substitutions)

    return exprs


def find_matching_parenthesis(string, index, delimiters=None):
    depth = 0
    if delimiters == None:
        delimiters = ('(', ')')
    for k in range(index, len(string)):
        if string[k] == delimiters[0]:
            depth += 1
            continue
        if string[k] == delimiters[1]:
            depth += -1
            if depth == 0:
                return k
    return -1


def substitute(string, substitutions):
    '''
    Input:
        string        (required) : a string or a list of strings
        substitutions (required) : a list with elements of the form (string,string)
                                   or ((string,list of strings),string)
    Output:
        A string that is the input string where any occurence of the left element
        of each pair in substitutions have been replaced with the corresponding right element.
        If the first element in the substitution is of the form (string,list of strings) then the substitution will only happen if the first element followed by one of the strings in the list in the second element.
    Remarks:
        Substitutions are made in the input order but if a substitutions left element is a
        substring of a preceding substitutions right element there will be no substitution.
        In most cases it is good practice to sort the substitutions by the length of the left
        element in descending order.
        Examples:
            substitute("abc bc c", [("abc","p"), ("bc","q"), ("c","r")])
            returns: "p q r"
            substitute("abc bc c", [("c","r"), ("bc","q"), ("abc","p")])
            returns: "abr br r"
            substitute("p bc c", [("p","abc"), ("bc","q"), ("c","r")])
            returns: "abc q c"
            substitute("p bc c", [("c","r"), ("bc","q"), ("p","abc")])
            returns: "abc br r"
    '''
    if isinstance(string, str):
        string = [string]

    # Perform substitutions
    new_string = []
    for part in string:
        if not isinstance(part, str):
            new_string.append(part)
        else:
            index = 0
            string_buffer = ""
            while index < len(part):
                matched_start = False
                for k, pair in enumerate(substitutions):
                    if isinstance(pair[0], tuple):
                        match = False
                        for look_ahead in pair[0][1]:
                            if part.startswith(pair[0][0]+look_ahead, index):
                                match = True
                                break
                        substitution_length = len(pair[0][0])
                    else:
                        match = part.startswith(pair[0], index)
                        substitution_length = len(pair[0])
                    if match:
                        matched_start = True
                        if len(string_buffer) > 0:
                            new_string.append(string_buffer)
                            string_buffer = ""
                        new_string.append(k)
                        index += substitution_length
                        break
                if not matched_start:
                    string_buffer += part[index]
                    index += 1
            if len(string_buffer) > 0:
                new_string.append(string_buffer)

    for k, elem in enumerate(new_string):
        if isinstance(elem, int):
            new_string[k] = substitutions[elem][1]

    return "".join(new_string)


def compute_relative_tolerance_from_significant_decimals(string):
    rtol = None
    string = string.strip()
    separators = "e*^ "
    separator_indices = []
    for separator in separators:
        if separator in string:
            separator_indices.append(string.index(separator))
        else:
            separator_indices.append(len(string))
    index = min(separator_indices)
    significant_characters = string[0:index].replace(".", "")
    index = 0
    for c in significant_characters:
        if c in "-0":
            index += 1
        else:
            break
    significant_characters = significant_characters[index:]
    rtol = 5*10**(-len(significant_characters))
    return rtol


# -------- (Sympy) Expression Parsing Utilities
class SymbolData(TypedDict):
    latex: str
    aliases: List[str]


SymbolDict = Dict[str, SymbolData]

symbol_latex_re = re.compile(
    r"(?P<start>\\\(|\$\$|\$)(?P<latex>.*?)(?P<end>\\\)|\$\$|\$)"
)


def sympy_symbols(symbols):
    """Create a mapping of local variables for parsing sympy expressions.

    Args:
        symbols (SymbolDict): A dictionary of sympy symbol strings to LaTeX
        symbol strings.

    Note:
        Only the sympy string is used in this function.

    Returns:
        Dict[str, Symbol]: A dictionary of sympy symbol strings to sympy
        Symbol objects.
    """
    return {k: Symbol(k) for k in symbols}


def extract_latex(symbol):
    """Returns the latex portion of a symbol string.

    Note:
        Only the first matched expression is returned.

    Args:
        symbol (str): The string to extract latex from.

    Returns:
        str: The latex string.
    """
    if (match := symbol_latex_re.search(symbol)) is None:
        return symbol

    return match.group("latex")


def latex_symbols(symbols):
    """Create a mapping between custom Symbol objects and LaTeX symbol strings.
    Used when parsing a sympy Expression to a LaTeX string.

    Args:
        symbols (SymbolDict): A dictionary of sympy symbol strings to LaTeX
        symbol strings.

    Returns:
        Dict[Symbol, str]: A dictionary of sympy Symbol objects to LaTeX
        strings.
    """
    symbol_dict = {
        Symbol(k): extract_latex(v["latex"])
        for (k, v) in symbols.items()
    }
    return symbol_dict


def sympy_to_latex(equation, symbols, settings=None):
    default_settings = {
        "symbol_names": latex_symbols(symbols),
        "ln_notation": True,
    }
    if settings is None:
        settings = default_settings
    else:
        for key in default_settings.keys():
            if key not in settings.keys():
                settings[key] = default_settings[key]
    latex_out = ModifiedLatexPrinter(settings).doprint(equation)
    return latex_out


def create_sympy_parsing_params(params, unsplittable_symbols=tuple(), symbol_assumptions=tuple()):
    '''
    Input:
        params               : evaluation function parameter dictionary
        unsplittable_symbols : list of strings that will not be split when parsing
                               even if implicit multiplication is used.
    Output:
        parsing_params: A dictionary that contains necessary info for the
                        parse_expression function.
    '''

    if "symbols" in params.keys():
        to_keep = []
        for symbol in params["symbols"].keys():
            if len(symbol) > 1:
                to_keep.append(symbol)
        unsplittable_symbols += tuple(to_keep)

    if params.get("specialFunctions", False) is True:
        from sympy import beta, gamma, zeta
    else:
        beta = Symbol("beta")
        gamma = Symbol("gamma")
        zeta = Symbol("zeta")
    if params.get("complexNumbers", False) is True:
        from sympy import I
    else:
        I = Symbol("I")
    if params.get("elementary_functions", False) is True:
        from sympy import E
        e = E
    else:
        E = Symbol("E")
    N = Symbol("N")
    O = Symbol("O")
    Q = Symbol("Q")
    S = Symbol("S")
    symbol_dict = {
        "beta": beta,
        "gamma": gamma,
        "zeta": zeta,
        "I": I,
        "N": N,
        "O": O,
        "Q": Q,
        "S": S,
        "E": E
    }

    for symbol in unsplittable_symbols:
        symbol_dict.update({symbol: Symbol(symbol)})

    symbol_dict.update(sympy_symbols(params.get("symbols", {})))

    strict_syntax = params.get("strict_syntax", True)

    parsing_params = {
        "unsplittable_symbols": unsplittable_symbols,
        "strict_syntax": strict_syntax,
        "symbol_dict": symbol_dict,
        "extra_transformations": tuple(),
        "elementary_functions": params.get("elementary_functions", False),
        "convention": params.get("convention", None),
        "simplify": params.get("simplify", False)
    }

    symbol_assumptions = list(symbol_assumptions)
    if "symbol_assumptions" in params.keys():
        symbol_assumptions_strings = params["symbol_assumptions"]
        index = symbol_assumptions_strings.find("(")
        while index > -1:
            index_match = find_matching_parenthesis(symbol_assumptions_strings, index)
            try:
                symbol_assumption = eval(symbol_assumptions_strings[index+1:index_match])
                symbol_assumptions.append(symbol_assumption)
            except (SyntaxError, TypeError) as e:
                raise Exception("List of symbol assumptions not written correctly.") from e
            index = symbol_assumptions_strings.find('(', index_match+1)
    for sym, ass in symbol_assumptions:
        try:
            parsing_params["symbol_dict"].update({sym: eval("Symbol('"+sym+"',"+ass+"=True)")})
        except Exception as e:
            raise Exception(f"Assumption {ass} for symbol {sym} caused a problem.") from e

    return parsing_params


def parse_expression(expr, parsing_params):
    '''
    Input:
        expr           : string to be parsed into a sympy expression
        parsing_params : dictionary that contains parsing parameters
    Output:
        sympy expression created by parsing expr configured according
        to the parameters in parsing_params
    '''

    expr = preprocess_according_to_chosen_convention(expr, parsing_params)

    strict_syntax = parsing_params.get("strict_syntax", False)
    extra_transformations = parsing_params.get("extra_transformations", ())
    unsplittable_symbols = parsing_params.get("unsplittable_symbols", ())
    symbol_dict = parsing_params.get("symbol_dict", {})
    separate_unsplittable_symbols = [(x, " "+x+" ") for x in unsplittable_symbols]
    # new approach
    substitutions = separate_unsplittable_symbols
    if parsing_params["elementary_functions"] is True:
        alias_substitutions = []
        for (name, alias_list) in elementary_functions_names+special_symbols_names:
            if name in expr:
                alias_substitutions += [(name, " "+name)]
            for alias in alias_list:
                if alias in expr:
                    alias_substitutions += [(alias, " "+name)]
        substitutions += alias_substitutions
    substitutions.sort(key=lambda x: -len(x[0]))
    expr = substitute(expr, substitutions)
    can_split = lambda x: False if x in unsplittable_symbols else _token_splittable(x)
    if strict_syntax:
        transformations = parser_transformations[0:5]+extra_transformations
    else:
        transformations = parser_transformations[0:5, 6]+extra_transformations+(split_symbols_custom(can_split),)+parser_transformations[8]
    if parsing_params.get("rationalise", False):
        transformations += parser_transformations[11]
    if parsing_params.get("simplify", False):
        parsed_expr = parse_expr(expr, transformations=transformations, local_dict=symbol_dict)
        parsed_expr = parsed_expr.simplify()
    else:
        parsed_expr = parse_expr(expr, transformations=transformations, local_dict=symbol_dict, evaluate=False)
    if not isinstance(parsed_expr, Basic):
        raise ValueError(f"Failed to parse Sympy expression `{expr}`")
    return parsed_expr
