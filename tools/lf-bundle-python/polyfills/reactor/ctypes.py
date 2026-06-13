"""Tiny reactor-python ctypes shim for pure-Python packages.

The CPython-WASI reactor artifact does not ship the native _ctypes extension.
Some pure-Python libraries only import ctypes to ask for primitive C integer
sizes (for example SymPy's optional gmpy detection). This shim intentionally
implements only that narrow surface; it is not a general ctypes replacement.
"""

class _CType:
    _size_ = 0


class c_long(_CType):
    _size_ = 8


class c_ulong(_CType):
    _size_ = 8


class c_int(_CType):
    _size_ = 4


class c_uint(_CType):
    _size_ = 4


def sizeof(obj):
    typ = obj if isinstance(obj, type) else type(obj)
    try:
        return int(typ._size_)
    except AttributeError as exc:
        raise TypeError(f"this ctypes shim does not know sizeof({typ!r})") from exc
