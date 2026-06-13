from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parent
MATRIX = ROOT / "capability-matrix.json"


def test_capability_matrix_schema_and_fixture_paths() -> None:
    rows = json.loads(MATRIX.read_text())
    assert rows, "matrix must not be empty"

    names = {row["fixture"] for row in rows}
    assert len(names) == len(rows), "fixture names must be unique"

    required = {
        "boilerplate-python",
        "compare-boolean",
        "array-equal",
        "is-similar",
        "symbolic-equal",
        "short-text-answer",
    }
    assert required <= names

    for row in rows:
        assert row["status"] in {
            "reactor-bundle",
            "reactor-bundle-pure-deps",
            "reactor-bundle-artifact-native",
            "pyodide-only",
            "out-of-scope",
        }
        assert isinstance(row["reactor_bundle"], bool)
        assert isinstance(row["pyodide_package"], bool)
        assert isinstance(row["requires_pure_python_deps"], list)
        assert isinstance(row["requires_reactor_artifact_deps"], list)
        assert isinstance(row["pyodide_only_reasons"], list)
        assert isinstance(row["verified_probes"], list)

        fixture_path = ROOT / row["fixture"]
        if row["reactor_bundle"]:
            assert fixture_path.exists(), f"reactor fixture missing: {fixture_path}"
            assert row["verified_probes"], f"reactor fixture lacks probes: {row['fixture']}"
        else:
            assert row["pyodide_only_reasons"], f"non-reactor row must explain why: {row['fixture']}"


def test_capability_matrix_matches_reactor_bundle_statuses() -> None:
    rows = {row["fixture"]: row for row in json.loads(MATRIX.read_text())}

    assert rows["boilerplate-python"]["status"] == "reactor-bundle"
    assert rows["compare-boolean"]["status"] == "reactor-bundle-pure-deps"
    assert rows["symbolic-equal"]["status"] == "reactor-bundle-pure-deps"
    assert rows["array-equal"]["status"] == "reactor-bundle-artifact-native"
    assert rows["is-similar"]["status"] == "reactor-bundle-artifact-native"
    assert rows["short-text-answer"]["status"] == "pyodide-only"
