"""Minimal lf_toolkit shim for adapter tests.

This is intentionally small and does **not** start a real server.
"""

from __future__ import annotations

from .shared.params import Params
from .evaluation import Result


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
    # Stubbed for adapter tests; real toolkit server behavior is provided by
    # production environments only.
    return server
