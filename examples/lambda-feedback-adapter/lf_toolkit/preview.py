from dataclasses import dataclass
from typing import Any, Dict, Optional


@dataclass
class Preview:
    markdown: Optional[str] = None
    html: Optional[str] = None
    data: Optional[Dict[str, Any]] = None


@dataclass
class Result:
    preview: Preview
    metadata: Optional[Dict[str, Any]] = None
