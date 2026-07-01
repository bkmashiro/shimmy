from dataclasses import dataclass
from typing import Any, Dict, Optional


@dataclass
class Result:
    is_correct: bool
    feedback: str = ""
    metadata: Optional[Dict[str, Any]] = None
