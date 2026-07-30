#!/usr/bin/env python3
"""Build NumPy's real _multiarray_umath core as WASI static archives."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shutil
import subprocess
import sys


PRODUCER_ROOT = pathlib.Path(__file__).resolve().parents[1]
DEFAULT_PATCH = PRODUCER_ROOT / "patches/numpy/static-core.json"
TARGET_PYTHON_SHIM = PRODUCER_ROOT / "tools/target_python_shim.py"


def apply_patch_set(source_root: pathlib.Path, patch_path: pathlib.Path) -> list[pathlib.Path]:
    document = json.loads(patch_path.read_text())
    if not isinstance(document, list) or not document:
        raise ValueError("NumPy patch set must be a non-empty array")
    changed: list[pathlib.Path] = []
    for item in document:
        if set(item) != {"path", "old", "new"}:
            raise ValueError("NumPy patch entry has unknown fields")
        relative = pathlib.PurePosixPath(item["path"])
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError(f"unsafe NumPy patch path: {relative}")
        target = source_root / relative
        source = target.read_text()
        old_count = source.count(item["old"])
        new_count = source.count(item["new"])
        if old_count == 1 and new_count == 0:
            target.write_text(source.replace(item["old"], item["new"]))
        elif old_count != 0 or new_count != 1:
            raise ValueError(f"NumPy patch no longer matches exactly: {relative}")
        changed.append(target)
    return changed


def _quote(value: pathlib.Path | str) -> str:
    return "'" + os.fspath(value).replace("'", "\\'") + "'"


def render_cross_file(
    *,
    wasi_sdk: pathlib.Path,
    wasmtime: pathlib.Path,
    native_python: pathlib.Path,
    cython: pathlib.Path,
    target_python_shim: pathlib.Path,
) -> str:
    bin_dir = wasi_sdk / "bin"
    python_command = f"[{_quote(native_python)}, {_quote(target_python_shim)}]"
    return f"""[binaries]
c = {_quote(bin_dir / 'wasm32-wasip1-clang')}
cpp = {_quote(bin_dir / 'wasm32-wasip1-clang++')}
ar = {_quote(bin_dir / 'llvm-ar')}
strip = {_quote(bin_dir / 'llvm-strip')}
cython = {_quote(cython)}
python = {python_command}
exe_wrapper = [{_quote(wasmtime)}, 'run']

[properties]
needs_exe_wrapper = true
skip_sanity_check = true
longdouble_format = 'IEEE_QUAD_LE'

[built-in options]
c_args = ['-O2', '-fno-exceptions']
cpp_args = ['-O2', '-fno-exceptions', '-fno-rtti']

[host_machine]
system = 'wasi'
cpu_family = 'wasm32'
cpu = 'wasm32'
endian = 'little'
"""


def _run(command: list[str], *, cwd: pathlib.Path, env: dict[str, str]) -> None:
    print("+", " ".join(command), flush=True)
    subprocess.run(command, cwd=cwd, env=env, check=True)


def build_static_core(
    *,
    numpy_root: pathlib.Path,
    cpython_root: pathlib.Path,
    target_build: pathlib.Path,
    wasi_sdk: pathlib.Path,
    wasmtime: pathlib.Path,
    native_python: pathlib.Path,
    cython: pathlib.Path,
    ninja_dir: pathlib.Path,
    build_dir: pathlib.Path,
    output_dir: pathlib.Path,
    jobs: int,
) -> pathlib.Path:
    apply_patch_set(numpy_root, DEFAULT_PATCH)
    if build_dir.exists():
        shutil.rmtree(build_dir)
    build_dir.mkdir(parents=True)
    output_dir.mkdir(parents=True, exist_ok=True)
    cross_file = build_dir / "shimmy-wasi.cross"
    cross_file.write_text(
        render_cross_file(
            wasi_sdk=wasi_sdk,
            wasmtime=wasmtime,
            native_python=native_python,
            cython=cython,
            target_python_shim=TARGET_PYTHON_SHIM,
        )
    )
    env = os.environ.copy()
    env.update(
        {
            "PATH": os.pathsep.join([os.fspath(ninja_dir), env["PATH"]]),
            "SHIMMY_TARGET_PYTHON_INCLUDE": os.fspath(cpython_root / "Include"),
            "SHIMMY_TARGET_PYTHON_PLATINCLUDE": os.fspath(target_build),
            "SOURCE_DATE_EPOCH": env.get("SOURCE_DATE_EPOCH", "1"),
            "PYTHONHASHSEED": "0",
        }
    )
    meson = numpy_root / "vendored-meson/meson/meson.py"
    setup = [
        os.fspath(native_python),
        os.fspath(meson),
        "setup",
        os.fspath(build_dir),
        os.fspath(numpy_root),
        "--cross-file",
        os.fspath(cross_file),
        "-Dblas=none",
        "-Dlapack=none",
        "-Ddisable-svml=true",
        "-Ddisable-highway=true",
        "-Ddisable-intel-sort=true",
        "-Ddisable-threading=true",
        "-Ddisable-optimization=true",
        "-Dcpu-baseline=min",
    ]
    _run(setup, cwd=numpy_root, env=env)
    _run(
        [
            os.fspath(native_python),
            os.fspath(meson),
            "compile",
            "-C",
            os.fspath(build_dir),
            "-j",
            str(jobs),
            "shimmy_numpy_multiarray_umath",
        ],
        cwd=numpy_root,
        env=env,
    )
    libraries = sorted(build_dir.rglob("*.a"))
    main = [path for path in libraries if path.name == "libshimmy_numpy_multiarray_umath.a"]
    if len(main) != 1:
        raise ValueError(f"expected one NumPy core archive, found {len(main)}")
    inventory = {
        "schema": "shimmy-numpy-static-libraries/v1",
        "main": os.fspath(main[0]),
        "libraries": [os.fspath(path) for path in libraries],
    }
    inventory_path = output_dir / "numpy-static-libraries.json"
    inventory_path.write_text(json.dumps(inventory, indent=2, sort_keys=True) + "\n")
    return inventory_path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    for name in (
        "numpy-root",
        "cpython-root",
        "target-build",
        "wasi-sdk",
        "wasmtime",
        "native-python",
        "cython",
        "ninja-dir",
        "build-dir",
        "output-dir",
    ):
        parser.add_argument(f"--{name}", type=pathlib.Path, required=True)
    parser.add_argument("--jobs", type=int, default=1)
    args = parser.parse_args(argv)
    if args.jobs < 1:
        parser.error("--jobs must be positive")
    build_static_core(
        numpy_root=args.numpy_root.resolve(),
        cpython_root=args.cpython_root.resolve(),
        target_build=args.target_build.resolve(),
        wasi_sdk=args.wasi_sdk.resolve(),
        wasmtime=args.wasmtime.resolve(),
        native_python=args.native_python.resolve(),
        cython=args.cython.resolve(),
        ninja_dir=args.ninja_dir.resolve(),
        build_dir=args.build_dir.resolve(),
        output_dir=args.output_dir.resolve(),
        jobs=args.jobs,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
