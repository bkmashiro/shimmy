from enum import Enum

class LexError(Exception):
    def __init__(self, char: str):
        self.unexpected = char
    def __str__(self) -> str:
        return f"unexpected token \'{self.unexpected}\'"

class TokenType(Enum):
    LBRACKET = 1
    RBRACKET = 2
    NOT = 3
    AND = 4
    OR = 5
    VARIABLE = 6
    UNKNOWN = 7
    EOF = 8
    XOR = 9

class Token:
    def __init__(self, type: TokenType, text: str, value=None):
        self.type = type
        self.text = text
        self.value = value

    def __str__(self) -> str:
        return str(self.type) if self.type != TokenType.VARIABLE else f"{str(self.type)} = {self.value}"

class Lexer:
    def __init__(self, input: str):
        self.input = input + " " # The space fixes an off-by-one in the lexer

    def lex(self) -> list[Token]:
        tokens = []
        if len(self.input) == 0:
            return tokens

        in_variable = False
        curr_variable = ""

        def get_token_type(char: str) -> TokenType:
            if char.isspace():
                return None
            elif char == '(':
                return TokenType.LBRACKET
            elif char == ')':
                return TokenType.RBRACKET
            elif char == '~':
                return TokenType.NOT
            elif char == '&':
                return TokenType.AND
            elif char == '|':
                return TokenType.OR
            elif char == '^':
                return TokenType.XOR
            elif char.isalpha():
                return TokenType.VARIABLE
            else:
                raise LexError(char)

        for char in self.input:
            if in_variable:
                if not (char.isalpha() or char.isnumeric() or char == '_'):
                    in_variable = False
                    tokens.append(Token(TokenType.VARIABLE, curr_variable, curr_variable))
                    curr_variable = ""
                    try:
                        curr_token_type = get_token_type(char)
                        if curr_token_type == None:
                            continue
                        if curr_token_type == TokenType.VARIABLE:
                            in_variable = True
                            curr_variable += char
                        else:
                            tokens.append(Token(curr_token_type, char))
                    except LexError:
                        raise
                else:
                    curr_variable += char

            else:
                try:
                    token_type = get_token_type(char)
                    if token_type == None:
                        continue

                    if token_type == TokenType.VARIABLE:
                        in_variable = True
                        curr_variable += char
                    else:
                        tokens.append(Token(token_type, char))
                except LexError:
                    raise

        tokens.append(Token(TokenType.EOF, ""))
        return tokens
