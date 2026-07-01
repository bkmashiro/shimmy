from lf_toolkit import create_server, run
from lf_toolkit.evaluation import Result

server = create_server()


@server.eval
def evaluation_function(response, answer, params):
    expected = params.get("expected", answer)
    is_correct = str(response).strip().lower() == str(expected).strip().lower()
    return Result(is_correct=is_correct, feedback="Correct" if is_correct else "Try again")


@server.preview
def preview_function(response, answer, params):
    return {"preview": {"response": response, "answer": answer, "expected": params.get("expected")}}


if __name__ == "__main__":
    run(server)
