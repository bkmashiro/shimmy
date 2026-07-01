from __future__ import annotations

from .evaluation import Result
from .preview import Preview


class Server:
    def __init__(self):
        self.eval_function = None
        self.preview_function = None

    def eval(self, fn):
        self.eval_function = fn
        return fn

    def preview(self, fn):
        self.preview_function = fn
        return fn


def create_server():
    return Server()


def run(server):
    raise RuntimeError("test lf_toolkit shim does not implement an HTTP server")


__all__ = ["Preview", "Result", "Server", "create_server", "run"]
