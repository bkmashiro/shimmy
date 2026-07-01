from lf_toolkit.evaluation import Result
from lf_toolkit.preview import Preview, Result as PreviewResult

from .parse import normalize, split_items


def evaluation_function(response, answer, params):
    mode = params.get("mode", "exact")
    if mode == "set":
        is_correct = split_items(response) == split_items(answer)
    else:
        is_correct = normalize(response) == normalize(answer)
    return Result(is_correct=is_correct, feedback="matched" if is_correct else "mismatch")


def preview_function(response, params):
    return PreviewResult(preview=Preview(markdown=f"Preview: {normalize(response)} / {params.get('mode', 'exact')}"))
