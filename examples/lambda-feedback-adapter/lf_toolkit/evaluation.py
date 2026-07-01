from dataclasses import dataclass
from typing import Any


@dataclass
class Result:
    is_correct: bool
    feedback: str = ""
    metadata: dict[str, Any] | None = None
