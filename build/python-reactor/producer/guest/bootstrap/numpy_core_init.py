# pyright: reportMissingImports=false

"""Shimmy NumPy core profile.

This facade intentionally exposes NumPy's real ``numpy._core`` API only. Optional
compiled subpackages such as ``numpy.linalg`` are outside this artifact profile.
"""

from ._globals import _CopyMode, _NoValue
from .version import version as __version__
from ._core import *  # noqa: F403
from ._core import __all__ as _core_all

__all__ = [*_core_all, "__version__"]
del _core_all
