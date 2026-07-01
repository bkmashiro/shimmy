from dataclasses import dataclass
from typing import Any


@dataclass
class Preview:
    markdown: str | None = None
    html: str | None = None
    data: dict[str, Any] | None = None


@dataclass
class Result:
    preview: Preview
    metadata: dict[str, Any] | None = None
