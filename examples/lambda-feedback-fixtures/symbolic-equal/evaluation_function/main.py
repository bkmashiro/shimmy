from lf_toolkit import create_server, run

from .evaluation import evaluation_function
from .preview import preview_function

server = create_server()
server.eval(evaluation_function)
server.preview(preview_function)

if __name__ == "__main__":
    run(server)
