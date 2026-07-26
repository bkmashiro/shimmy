"""Guest-side NumPy binary128 canary for a future clean reactor handoff."""

import numpy as np


_one = np.longdouble("1")
_beyond_binary64 = np.longdouble("1.0000000000000000000000000000000002")
_longdouble = np.finfo(np.longdouble)
_double = np.finfo(np.double)

result = {
    "longdouble_itemsize": int(np.dtype(np.longdouble).itemsize),
    "longdouble_nmant": int(_longdouble.nmant),
    "double_nmant": int(_double.nmant),
    "preserves_extra_precision": bool(_beyond_binary64 > _one),
    "narrows_to_double_one": bool(float(_beyond_binary64) == 1.0),
    "epsilon_is_narrower": bool(_longdouble.eps < _double.eps),
    "rendered_value": str(_beyond_binary64),
}

assert result["longdouble_itemsize"] == 16
assert result["longdouble_nmant"] >= 112
assert result["longdouble_nmant"] > result["double_nmant"]
assert result["preserves_extra_precision"]
assert result["narrows_to_double_one"]
assert result["epsilon_is_narrower"]
