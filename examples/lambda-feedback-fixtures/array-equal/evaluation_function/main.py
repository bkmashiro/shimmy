from lf_toolkit import create_server
from .evaluation import evaluation_function

server = create_server()
server.eval(evaluation_function)
