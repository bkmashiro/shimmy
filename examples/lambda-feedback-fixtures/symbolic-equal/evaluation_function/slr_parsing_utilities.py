# -----------------
# -----------------
# UTILITY FUNCTIONS
# -----------------
# -----------------

# -----------------
# Scanner utilities
# -----------------
import re


def catch_undefined(label, content, original, start, end):
    return Token(label, content, original, start, end)

# -----------------
# Parser utilities
# -----------------


# Syntax tree building utilities
def package(production, output, tag_handler):
    label = production[0].label
    handle = production[1]
    children = output[-len(handle):]
    output = output[0:(-len(handle))]
    package_content = "".join(str(children))
    new_package = ExprNode(Token(label, package_content, children[0].original, children[0].start, children[0].end), children, tag_handler=tag_handler)
    output.append(new_package)
    return output


def append(production, output, tag_handler):
    handle = production[1]
    children = output[1-len(handle):]
    output = output[0:(1-len(handle))]
    output[-1].children += children
    return output


def join(production, output, tag_handler):
    label = production[0].label
    handle = production[1]
    joined_content = "".join([node.content for node in output[-len(handle):]])
    joined_end = output[-1].end
    output = output[0:(1-len(handle))]
    output[-1].label = label
    output[-1].content = joined_content
    output[-1].end = joined_end
    return output


def create_node(production, output, tag_handler):
    a = output.pop()
    node = ExprNode(a, [], tag_handler=tag_handler)
    output.append(node)
    return output


def relabel(production, output, tag_handler):
    a = output.pop()
    output.append(Token(production[0].label, a.content, a.original, a.start, a.end))
    return output


def group(number_of_elements, empty=False, delimiters=["", ""]):
    if number_of_elements < 1:
        raise Exception("Groups must have at least one element.")

    def wrap(production, output, tag_handler):
        if empty:
            content = output[-number_of_elements:]
            output = output[0:-number_of_elements]
            end_delim = Token("START_DELIMITER", delimiters[1], content[0].original, content[0].start, content[0].end)
            start_delim = Token("END_DELIMITER", delimiters[0], content[0].original, content[0].start, content[0].end)
        else:
            end_delim = output.pop()
            content = output[-number_of_elements:]
            output = output[0:-number_of_elements]
            start_delim = output.pop()
        for k, elem in enumerate(content):
            if isinstance(elem, Token) and not isinstance(elem, ExprNode):
                content[k] = ExprNode(elem, [], tag_handler=tag_handler)
        output.append(
            ExprNode(
                Token(
                    "GROUP",
                    [start_delim.content, end_delim.content],
                    content[0].original,
                    start_delim.start, end_delim.end
                    ),
                content, traverse_step=traverse_group, tag_handler=tag_handler)
            )
        return output

    return wrap


def operate(number_of_elements, empty=False):
    if number_of_elements < 0:
        raise Exception("Operations must have at least one argument.")

    def wrap(production, output, tag_handler):
        if empty:
            end_index = output[-1].end
            content = output[-number_of_elements:]
            output = output[0:-number_of_elements]
        else:
            end_delim = output.pop()
            end_index = end_delim.end
            content = output[-number_of_elements:]
            output = output[0:-number_of_elements]
            start_delim = output.pop()
        for k, elem in enumerate(content):
            if isinstance(elem, Token) and not isinstance(elem, ExprNode):
                content[k] = ExprNode(elem, [], tag_handler=tag_handler)
        operation = output.pop()
        node = ExprNode(operation, content, tag_handler=tag_handler)
        node.end = end_index
        output.append(node)
        return output

    return wrap


def infix(production, output, tag_handler):
    right = output.pop()
    operator = output.pop()
    left = output.pop()
    output.append(ExprNode(operator, [left, right], traverse_step=traverse_infix, tag_handler=tag_handler))
    return output


def insert_infix(content, label):
    def apply(production, output, tag_handler):
        operator_token = Token(label, content, output[-1].original, len(output[-1].original), -1)
        return infix(production, output[0:-1]+[operator_token]+[output[-1]], tag_handler=tag_handler)
    return apply


def compose(*funcs):
    def composed_functions(production, output, tag_handler):
        for f in reversed(funcs):
            output = f(production, output, tag_handler)
        return output
    return composed_functions


def flatten(production, output, tag_handler):
    node = output[-1]
    flattened_children = []
    for child in node.children:
        if node.label == child.label and node.content == child.content:
            flattened_children += child.children
        else:
            flattened_children.append(child)
    node.children = flattened_children
    return output


# Tag management utilities

def tag_rule_union(x, y):
    return x | y


def tag_rule_intersection(x, y):
    return x & y


def tag_transfer(node, rule=tag_rule_union):
    tags = set()
    if len(node.children) > 0:
        tags = node.children[0].tags
        for child in node.children[1:]:
            tags = rule(tags, child.tags)
    return tags


def tag_removal(node, tag, rule=lambda x: True):
    if rule(node.tags) and tag in node.tags:
        node.tags.remove(tag)
    return node


def tag(node, tag=None, rule=lambda x: True):
    if tag is not None:
        if node.tags is None:
            node.tags = set()
        if rule(node.tags):
            node.tags.add(tag)
    return node


def tag_replace(node, old_tag, new_tag):
    if old_tag in node.tags:
        node = tag(node, new_tag)
        node = tag_removal(node, old_tag)
    return node

# ------------------------
# Node traversal utilities
# ------------------------


def traverse_prefix(expr_node, action):
    out = [(True, action(expr_node))]
    for x in expr_node.children:
        out += [(False, x)]
    return out


def traverse_postfix(expr_node, action):
    out = []
    for x in expr_node.children:
        out += [(False, x)]
    return out+[(True, action(expr_node))]


def traverse_infix(expr_node, action):
    return [(False, expr_node.children[0])]+[(True, action(expr_node))]+[(False, expr_node.children[1])]


def traverse_group(expr_node, action):
    out = [(True, action(expr_node)[0])]
    for x in expr_node.children:
        out += [(False, x)]
    return out+[(True, action(expr_node)[1])]


# error handling utilities
def new_root_on_error(parser, stack, a, input_tokens, tokens, output):
    a = parser.end_token
    return stack, a, input_tokens, tokens, output


def discard_output_until_on_error(condition):
    def error(parser, stack, a, input_tokens, tokens, output):
        while not condition(output[-1]):
            output.pop()
        a = parser.end_token
        return stack, a, input_tokens, tokens, output
    return error

# --------------------------
# Parser generator utilities
# --------------------------


def SLR_expression_parser(nodes=[], infix_operators=[], delimiters=[], undefined=None, costum_tokens=[], costum_productions=[], group_node=None, expression_node=None, start=None, null=None, end=None, error_handler=[]):
    infix_operators_dictionary = dict()
    unique_infix_operator_symbols = []
    for (symbol, label) in infix_operators:
        if label in infix_operators_dictionary.keys():
            infix_operators_dictionary[label] += [symbol]
        else:
            unique_infix_operator_symbols += [symbol]
            infix_operators_dictionary.update({label: [symbol]})
    infix_operators_token = []
    for (label, symbols) in infix_operators_dictionary.items():
        infix_operators_token.append(((" *("+"|".join([re.escape(s) for s in symbols])+") *"), label))

    if undefined is None:
        undefined_symbol = "UNDEFINED"
        undefined = (undefined_symbol, undefined_symbol, catch_undefined)
    else:
        undefined += (catch_undefined,)

    if expression_node is None:
        expression_node_symbol = "EXPRESSION_NODE"
        expression_node = (expression_node_symbol, expression_node_symbol, None)
    else:
        undefined += (None,)

    if group_node is None:
        group_node_symbol = "GROUP_NODE"
        group_node = (group_node_symbol, group_node_symbol, None)

    if start is None:
        start_symbol = "START"
        start = (start_symbol, start_symbol)

    if end is None:
        end_symbol = "END"
        end = (end_symbol, end_symbol)

    if null is None:
        null_symbol = "NULL"
        null = (null_symbol, null_symbol)

    token_list = [undefined, null, expression_node, start, end]+nodes+infix_operators_token+costum_tokens

    productions = [(start[0], expression_node[0], relabel)]
    productions += [(expression_node[0], n[0], create_node) for n in nodes]
    productions += [(expression_node[0], undefined[0], create_node)]
    productions += [(expression_node[0], expression_node[0]+operator+expression_node[0], infix) for operator in [op[0] for op in unique_infix_operator_symbols]]

    for (delims, action) in delimiters:
        token_list += [(re.escape(delims[0])+" *", "START_DELIMITER"), (" *"+re.escape(delims[1]), "END_DELIMITER")]
        productions += [(expression_node[0], delims[0]+expression_node[0]+delims[1], action)]

    productions += costum_productions

    return SLR_Parser(token_list, productions, start[1], end[1], null[1], error_handler=error_handler)

# -------
# -------
# CLASSES
# -------
# -------


class Token:

    def __init__(self, label, content, original, start, end):
        self.label = label
        self.content = content
        self.original = original
        self.start = start
        self.end = end
        return

    def __eq__(self, other):
        return isinstance(other, Token) and self.label == other.label

    def __hash__(self):
        return hash(self.label)

    def __str__(self):
        return str(self.label)+": "+str(self.content)

    def __repr__(self):
        # REMARK(KarlLundengaard): This is not a good repr function, but it means that the most
        # relevant info is printed in the watch window of my preferred debugger
        return str(self.label)+": "+str(self.content)


class ExprNode(Token):

    def __init__(self, token, children, tag_handler=None, tags=set(), traverse_step=traverse_prefix):
        super().__init__(token.label, token.content, token.original, token.start, token.end)
        self.tags = set()
        self.children = []
        for child in children:
            if isinstance(child, ExprNode):
                self.children.append(child)
            elif isinstance(child, Token):
                self.children.append(ExprNode(child, []))
            else:
                raise Exception(f"Invalid child {str(child)}")
        self._traverse_step = traverse_step
        if tag_handler is not None:
            self.tags = tag_handler(self)
        else:
            self.tags = tags
        return

    def copy(self):
        token = Token(self.label, self.content, self.original, self.start, self.end)
        children = []
        for child in self.children:
            children.append(child.copy())
        return ExprNode(token, children, tags=self.tags, traverse_step=self._traverse_step)

    def tree_string(self):
        s = str(self)
        for k, child in enumerate(self.children):
            padding = "\n|   " if k < len(self.children)-1 else "\n    "
            s += "\n"+str(k)+": "+child.tree_string().replace("\n", padding)
        return s

    def content_string(self, max_depth=None):
        output = self.traverse(lambda x: x.content, max_depth)
        return "".join(output)

    def traverse(self, action, max_depth=None):
        stack = [x+(0,) for x in self._traverse_step(self, action)[::-1]]
        output = []
        while len(stack) > 0:
            (is_output, elem, depth) = stack.pop()
            if max_depth is None or depth <= max_depth:
                if is_output:
                    output.append(elem)
                else:
                    stack += [x+(depth+1,) for x in elem._traverse_step(elem, action)[::-1]]
        return output

    def original_string(self):
        left_children = self.children
        right_children = self.children
        start = self.start
        end = self.end
        while len(left_children) > 0:
            start = min(start, left_children[0].start)
            left_children = left_children[0].children
        while len(right_children) > 0:
            end = max(end, right_children[-1].end)
            right_children = right_children[-1].children
        return self.original[start:end+1]

    def __str__(self):
        tags = " tags: "
        tags += str(self.tags) if len(self.tags) > 1 else " {}"
        return str(self.label)+": "+str(self.content)+tags

    def __repr__(self):
        # REMARK(KarlLundengaard): This is not a good repr function, but it means that the most
        # relevant info is printed in the watch window of my preferred debugger
        return str(self.label)+": "+str(self.content)+" tags: "+str(self.tags)


class SLR_Parser:

    def default_error_action(parser, stack, a, input_tokens, tokens, output):
        m = 70
        raise Exception(
            f"\n{'-'*m}\n" +
            f"ERROR:\n{'-'*m}\n" +
            f"accepted: {input_tokens[:-len(tokens)]}\n" +
            f"current: {a}, {parser._symbols_index[a]}\n" +
            f"remaining: {tokens}\n" +
            f"stack: {stack}\n" +
            f"output: {output}\n" +
            f"state: {parser.state_string(parser._states_index[stack[-1]])}\n" +
            f"{'-'*m}")

    def __init__(self, token_list, productions, start_symbol, end_symbol, null_symbol, error_handler=[], tag_handler=tag_transfer):
        self.token_list = token_list
        self.token_list.sort(key=lambda x: -len(x[0]))
        self.productions = productions
        self.start_symbol = start_symbol
        self.end_symbol = end_symbol
        self.null_symbol = null_symbol
        self.start_token = self.scan(start_symbol, mode="bnf")[0]
        self.end_token = self.scan(end_symbol, mode="bnf")[0]
        self.null_token = self.scan(null_symbol, mode="bnf")[0]
        start_token = self.start_token
        end_token = self.end_token
        null_token = self.null_token
        self.error_handler = error_handler
        self.tag_handler = tag_handler

        # Check if there are any duplicate productions
        checked_productions = []
        duplicate_error_string = []
        for prod in [(x[0], x[1]) for x in productions]:
            if prod in checked_productions:
                duplicate_error_string.append(f"duplicate: {prod}")
            checked_productions.append(prod)
        if len(duplicate_error_string) > 0:
            raise Exception("There are duplicate productions:\n" + "\n".join(duplicate_error_string))

        # Tokenize productions
        productions_token = [
            (self.scan(x[0], mode="bnf")[0], self.scan(x[1], mode="bnf")) for x in productions
        ]
        self.productions_token = productions_token

        # Analyse productions to find terminals and non-terminals
        non_terminals_token = []
        terminals_token = [self.end_token, self.null_token]
        for token in [prod[0] for prod in productions_token]:
            if token not in non_terminals_token:
                non_terminals_token.append(token)
        for production in productions_token:
            for token in production[1]:
                if token not in terminals_token and token not in non_terminals_token:
                    terminals_token.append(token)
        self.symbols = terminals_token+non_terminals_token
        self.terminals_token = terminals_token
        self.non_terminals_token = non_terminals_token

        # Create reductions dictionary
        self.reductions = {tuple(productions_token[k][1]): productions[k][2] for k in range(0, len(productions))}

        # Compute dictionary with FIRST for all single tokens
        first_dict = {**{x: [x] for x in terminals_token}, **{x: [] for x in non_terminals_token}}
        lengths = [-1]*len(non_terminals_token)
        any_first_changed = True
        while any_first_changed:
            any_first_changed = False
            for k, x in enumerate(non_terminals_token):
                if lengths[k] != len(first_dict[x]):
                    lengths[k] = len(first_dict[x])
                    any_first_changed = True
            for nt in non_terminals_token:
                prods = [x[1] for x in productions_token if x[0] == nt]
                for prod in prods:
                    for token in prod:
                        for x in first_dict[token]:
                            if x not in first_dict[nt]:
                                first_dict[nt].append(x)
                        if null_token not in first_dict[token]:
                            break
        self._first_dict = first_dict

        # Compute dictionary with FOLLOW for all non_terminals
        first = self.first
        follow = {x: [] for x in non_terminals_token}
        follow[start_token].append(end_token)
        lengths = [-1]*len(non_terminals_token)
        while lengths != [len(follow[x]) for x in non_terminals_token]:
            lengths = [len(follow[x]) for x in non_terminals_token]
            for (head, body) in productions_token:
                for k, token in enumerate(body):
                    if token in non_terminals_token:
                        if null_token in self.first(body[k+1:]) or len(body[k+1:]) == 0:
                            for item in follow[head]:
                                if item not in follow[token]:
                                    follow[token].append(item)
                        for item in first(body[k+1:]):
                            if item != null_token and item not in follow[token]:
                                follow[token].append(item)
        self._follow = follow

        # Compute all states and the transitions between them
        closure = self.closure
        compute_transitions = self.compute_transitions
        start_productions = tuple([(k, 0) for k in range(0, len(productions_token)) if productions_token[k][0] == start_token])
        states = {start_productions: closure(start_productions)}
        transitions = {}
        new_states = [start_productions]
        while len(new_states) > 0:
            state = new_states.pop(0)
            trans = compute_transitions(closure(list(state)))
            transitions.update({state: trans})
            for t in trans:
                if tuple(t[1]) not in states.keys():
                    states.update({tuple(t[1]): closure(t[1])})
                    new_states.append(tuple(t[1]))
        self.states = states
        self.transitions = transitions

        # Create index dictionaries to simplify state table construction
        states_index = {}
        for i, s in enumerate(states):
            states_index.update({s: i, i: s})
        self._states_index = states_index

        symbols_index = {}
        symbols = terminals_token+non_terminals_token
        for j, h in enumerate(symbols):
            symbols_index.update({h: j, j: h})
        self._symbols_index = symbols_index

        # Compute parsing table
        parsing_table = []
        for i in range(0, len(states)):
            parsing_table.append([])
            for j in range(0, len(symbols)):
                parsing_table[i].append([])

        for state in states:
            for (production_index, dot_index) in closure(state):
                # Fill in shift actions and goto table
                (head, body) = productions_token[production_index]
                if dot_index < len(body):
                    a = body[dot_index]
                    table_entry = parsing_table[states_index[state]][symbols_index[a]]
                    for (symbol, next_state) in transitions[state]:
                        if symbol == a:
                            table_entry.append((production_index, states_index[tuple(next_state)]))
                            break
                elif head == self.start_token:
                    parsing_table[states_index[state]][symbols_index[self.end_token]].append((production_index, len(self.states)))
                else:
                    # Fill in reduce actions
                    for a in follow[head]:
                        table_entry = parsing_table[states_index[state]][symbols_index[a]]
                        table_entry.append((production_index, len(states)+production_index))

        # Choose correct table entry based on precedence,
        # precedence is determined by location in productions
        # array (higher index in array means higher precedence)
        for i in range(0, len(states)):
            for j in range(0, len(symbols)):
                table_entry = parsing_table[i][j]
                if len(table_entry) == 0:
                    # items_token gives a list of pairs that contain the parts of the production before and after the current point
                    items_token = [(productions_token[x[0]][1][0:x[1]], productions_token[x[0]][1][x[1]:]) for x in states_index[i]]
                    next_symbol = symbols_index[j]
                    parsing_table[i][j] = -1
                    for (index, condition) in enumerate([x[0] for x in self.error_handler], 2):
                        if condition(items_token, next_symbol):
                            parsing_table[i][j] = -index
                            break
                elif len(table_entry) == 1:
                    parsing_table[i][j] = table_entry[0][1]
                else:
                    precedence = table_entry[0][0]
                    index = 0
                    for k, action in enumerate(table_entry[1:], 1):
                        if precedence < action[0]:
                            precedence = action[0]
                            index = k
                    parsing_table[i][j] = table_entry[index][1]
        self.parsing_table = parsing_table

        # Check for unreachable states and reductions
        unreachable = list(range(0, len(states)+len(self.reductions)))
        for i in range(0, len(states)):
            for j in range(0, len(symbols)):
                if parsing_table[i][j] in unreachable:
                    unreachable.remove(parsing_table[i][j])
        self.parsing_table = parsing_table

        if len(unreachable) > 1:
            print("Unreachable states:")
            for x in [y for y in unreachable if y < len(states)]:
                print("\t"+self.state_string(self._states_index[x]))
            print("Unreachable reductions:")
            for x in [y for y in unreachable if y >= len(states) and y < len(states)+len(productions_token)]:
                print("\t"+self.productions[x-len(states)][0]+"-->"+str(self.productions[x-len(states)][1]))

        return

    def scan(self, expr, mode="expression"):
        token_list = self.token_list
        tokens = []

        new_token = lambda l, c, s, e: Token(l, c, expr, s, e)

        token_catch_undefined = [x for x in token_list if len(x) > 2 and x[2] == catch_undefined]
        if len(token_catch_undefined) > 1:
            raise Exception("Only one token type can be used to catch undefined lexemes.")
        elif len(token_catch_undefined) < 1:
            token_catch_undefined = None
        else:
            token_catch_undefined = token_catch_undefined[0]

        if "expression" in mode:
            token_rules = [x for x in token_list if len(x) > 2 and x[2] not in {None, catch_undefined}]
            token_symbols = [x for x in token_list if len(x) == 2]
        elif "bnf" in mode:
            token_rules = []
            token_symbols = [(x[0], x[1]) for x in token_list]

        index = 0
        string = ""
        while index-len(string) < len(expr):
            end_token = None
            end_token_length = 0
            content = ""
            label = None
            for (re_content, current_label) in token_symbols:
                match_content = re.match(re_content, expr[index:])
                if match_content is not None:
                    current_content = match_content.group()
                    if len(current_content) > end_token_length:
                        content = current_content
                        end_token_length = len(content)
                        label = current_label
            for token in token_rules:
                current_label = token[1]
                match_rule, match_content = token[2](expr[index:])
                if match_rule is not None:
                    if len(match_rule) > end_token_length:
                        content = match_content
                        end_token_length = len(match_rule)
                        label = current_label
            if label is None:
                string = string+expr[index]
                index += 1
            else:
                end_token = new_token(label, content, index, index+end_token_length-1)
            if len(string) > 0 and (end_token is not None or index >= len(expr)):
                if token_catch_undefined is not None:
                    tokens.append(token_catch_undefined[2](token_catch_undefined[1], string, expr, index-len(string), index-1))
                else:
                    raise Exception(f"Undefined input: {string}")
                string = ""
            if end_token is not None:
                tokens.append(end_token)
                index += end_token_length
        return tokens

    def closure(self, item_set):
        non_terminals = self.non_terminals_token
        productions = self.productions_token
        # Items are represented as (i,j) where i indicates index
        # in production list and j position of inserted dot
        closure_set = []
        offset = -1
        new_items = item_set
        added_to_closure = [False]*len(non_terminals)
        while len(new_items) > 0:
            closure_set += new_items
            offset += len(new_items)
            new_items = []
            for item in closure_set:
                i = item[0]
                j = item[1]
                follow = None
                if j < len(productions[i][1]):
                    for k, nt in enumerate(non_terminals):
                        if productions[i][1][j] == nt:
                            follow = k
                            break
                    if follow is not None and not added_to_closure[follow]:
                        for k, production in enumerate(productions):
                            if production[0] == non_terminals[follow]:
                                new_items.append((k, 0))
                        added_to_closure[follow] = True
        return closure_set

    def compute_transitions(self, item_set):
        productions_token = self.productions_token
        transitions = []
        for item in item_set:
            i = item[0]
            j = item[1]
            if j < len(productions_token[i][1]):
                token = productions_token[i][1][j]
                if token not in [x[0] for x in transitions]:
                    transitions.append((token, [(i, j+1)]))
                else:
                    transitions[[x[0] for x in transitions].index(token)][1].append((i, j+1))
        return transitions

    def first(self, tokens):
        # Computes FIRST for strings of tokens
        null_token = self.null_token
        first_dict = self._first_dict
        if len(tokens) == 1:
            return first_dict[tokens[0]]
        fs = []
        for token in tokens:
            for item in first_dict[token]:
                if item not in fs and item != null_token:
                    fs.append(item)
            if token != null_token:
                break
        return fs

    def parsing_table_to_string(self):
        parsing_table = self.parsing_table
        symbols = [x.content for x in self.terminals_token+self.non_terminals_token]
        states = self.states
        parsing_table_string = ["\t"+"\t".join(symbols)+"\n"]

        for i in range(0, len(parsing_table)):
            parsing_table_string += [str(i)+"\t"]
            for j in range(0, len(parsing_table[i])):
                if parsing_table[i][j] < 0:
                    parsing_table_string += ['e'+str(-parsing_table[i][j])]
                elif parsing_table[i][j] < len(states):
                    parsing_table_string += ['s'+str(parsing_table[i][j])]
                else:
                    parsing_table_string += ['r'+str(parsing_table[i][j]-len(states))]
                parsing_table_string += ['\t']
            parsing_table_string += ['\n']

        return "".join(parsing_table_string)

    def state_string(self, state):
        items = self.closure(state)
        prod_strings = []
        for k in range(0, len(items)):
            production = self.productions_token[items[k][0]]
            dot_index = items[k][1]
            prod_string = "".join(x.content for x in production[1][0:dot_index])+"."+"".join(x.content for x in production[1][dot_index:])
            prod_strings.append(prod_string)
        return "I"+str(self._states_index[state])+": ("+", ".join(prod_strings)+")"

    def state_string_list(self, state):
        items = self.closure(state)
        prod_strings = []
        for k in range(0, len(items)):
            production = self.productions_token[items[k][0]]
            dot_index = items[k][1]
            prod_string = "".join(x.content for x in production[1][0:dot_index])+"."+"".join(x.content for x in production[1][dot_index:])
            prod_strings.append(prod_string)
        return ["I"+str(self._states_index[state])]+prod_strings

    def parsing_action(self, s, a):
        return self.parsing_table[s][self._symbols_index[a]]

    def parse(self, input_tokens, verbose=False):
        productions_token = self.productions_token
        tokens = list(input_tokens)
        if tokens[-1] != self.end_token:
            tokens += [self.end_token]
        a = tokens.pop(0)
        stack = [0]
        output = []
        while True:
            parse_action = self.parsing_action(stack[-1], a)
            while parse_action < 0:
                if parse_action == -1:
                    self.default_error_action(stack, a, input_tokens, tokens, output)
                else:
                    stack, a, input_tokens, tokens, output = self.error_handler[-2-parse_action][1](self, stack, a, input_tokens, tokens, output)
                parse_action = self.parsing_action(stack[-1], a)
            if parse_action < len(self.states):
                stack.append(parse_action)
                output.append(a)
                if verbose:
                    print("shift and transition to: "+self.state_string(self._states_index[parse_action])+"  \t"+str(output))
                a = tokens.pop(0)
            elif parse_action == len(self.states):
                if verbose:
                    print("ACCEPT")
                if len(tokens) > 0 and tokens != [self.end_token]:
                    output += self.parse(tokens)
                break
            elif parse_action <= len(self.states)+len(self.productions_token):
                production = productions_token[parse_action-len(self.states)]
                reduction = self.reductions[tuple(production[1])]
                output = reduction(production, output, self.tag_handler)
                # print("-----------------------")
                # print(output[0].tree_string())
                # print("-----------------------")
                stack = stack[0:-len(production[1])]
                stack.append(self.parsing_action(stack[-1], production[0]))
                if verbose:
                    print("reduce by: "+str(production[0].content)+" --> "+"".join([x.content for x in production[1]])+"  \t"+str(output))
                    print("new state: "+self.state_string(self._states_index[stack[-1]]))
                    print("next input: "+str(a))
            else:
                m = 70
                raise Exception(f"{'-'*m}\nINVALID ENTRY:\n{'-'*m}\naccepted: {input_tokens[:-len(tokens)]}\ncurrent: {a}\nremaining: {tokens}\nstack: {stack}\noutput: {output}\n{'-'*m}")
                break
        return output

# -------
# -------
# TESTING
# -------
# -------


if __name__ == "__main__":

    productions = [
        ("S", "E",   create_node),
        ("E", "E+E", infix),
        ("E", "E*E", infix),
        ("E", "(E)", group),
        ("E", "I",   relabel)
    ]

    start_symbol = "S"
    end_symbol = "$"
    null_symbol = "e"

    infix_operators = ["+", "*"]
    delimiters = ["(", ")"]
    token_list = [(start_symbol, "START"), ("E", "EXPRESSION"), ("I", "IDENTIFIER")]\
        + [(x, "INFIX"+x) for x in infix_operators]\
        + [("(", "START_DELIMITER"), (")", "END_DELIMITER")]\
        + [(end_symbol, "END"), (null_symbol, "NULL")]

    test_parser = SLR_Parser(token_list, productions, start_symbol, end_symbol, null_symbol)
    print(test_parser.parsing_table_to_string())

    ref_string = '\t+\t*\t(\t)\tI\t$\te\tS\tE\n0\tx\tx\ts2\tx\ts3\tx\tx\tx\ts1\t\n1\ts4\ts5\tx\tx\tx\tr0\tx\tx\tx\t\n2\tx\tx\ts2\tx\ts3\tx\tx\tx\ts6\t\n3\tr4\tr4\tx\tr4\tx\tr4\tx\tx\tx\t\n4\tx\tx\ts2\tx\ts3\tx\tx\tx\ts7\t\n5\tx\tx\ts2\tx\ts3\tx\tx\tx\ts8\t\n6\ts4\ts5\tx\ts9\tx\tx\tx\tx\tx\t\n7\tr1\ts5\tx\tr1\tx\tr1\tx\tx\tx\t\n8\tr2\tr2\tx\tr2\tx\tr2\tx\tx\tx\t\n9\tr3\tr3\tx\tr3\tx\tr3\tx\tx\tx\t\n'

    if ref_string != test_parser.parsing_table_to_string():
        print(" ***************************************\n * WARNING: test parsing table changed *\n ***************************************")

    test_tokens = test_parser.scan("(I+I)*I+I")
    print([str(token.label) for token in test_tokens])

    output = test_parser.parse(test_tokens)
    print(output)
    print(output[0].tree_string())
