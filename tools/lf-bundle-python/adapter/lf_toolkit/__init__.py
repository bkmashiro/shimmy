"""Minimal lf_toolkit surface needed by compatibility fixtures."""

from __future__ import annotations

from .evaluation import Result
from .shared.params import Params


class _Server:
    def __init__(self):
        self.eval_fn = None
        self.preview_fn = None

    def eval(self, fn):
        self.eval_fn = fn
        return self

    def preview(self, fn):
        self.preview_fn = fn
        return self


def create_server():
    return _Server()


def run(server):
    return server
