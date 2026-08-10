from __future__ import annotations

from .shared.params import Params


class Preview:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)

    def dict(self) -> dict:
        return dict(self.__dict__)


class Result:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)

    def dict(self) -> dict:
        return dict(self.__dict__)
