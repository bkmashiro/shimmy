class Term:
    def __init__(self, term, op: bool = False):
        self.term = term
        self.op = op

    def __str__(self) -> str:
        out = "" if not self.op else "~"
        if isinstance(self.term, str):
            return out + self.term
        else:
            return out + f"({str(self.term)})"

    def to_latex(self) -> str:
        out = ""
        if isinstance(self.term, str):
            if len(self.term) == 1:
                out += self.term
            else:
                out += f"\\mathrm{{{self.term}}}"
        else:
            out += f"\\left( {self.term.to_latex()} \\right)"
        if self.op:
            return f"\\overline{{{out}}}"
        else:
            return out


class Prod:
    def __init__(self, left: Term, right: list[Term] = None):
        self.left = left
        self.right = right

    def __str__(self) -> str:
        out = str(self.left)
        for right in self.right:
            out += f" & {str(right)}"
        return out

    def to_latex(self) -> str:
        out = self.left.to_latex()
        for term in self.right:
            out += " \\cdot "
            out += term.to_latex()
        return out


class Expr:
    def __init__(self, left: Prod, right: list[bool, Prod] = None):
        self.left = left
        self.right = right

    def __str__(self) -> str:
        out = str(self.left)
        for xor, right in self.right:
            out += f" {'^' if xor else '|'} {str(right)}"
        return out

    def to_latex(self) -> str:
        out = self.left.to_latex()
        for xor, prod in self.right:
            out += " \\oplus " if xor else " + "
            out += prod.to_latex()

        return out
