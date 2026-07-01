"use strict";

/**
 * Pyodide/Node.js runner for shimmy eval functions.
 *
 * Protocol: go-ethereum JSON-RPC 2.0 over stdio, framed with LSP-style headers.
 *
 * Each message (both directions) is framed as:
 *   Content-Length: <N>\r\n
 *   <N bytes of JSON>
 *
 * Request JSON (from shimmy):
 *   {"jsonrpc":"2.0","id":<id>,"method":"<method>","params":[{...}]}
 *
 * Response JSON (to shimmy):
 *   {"jsonrpc":"2.0","id":<id>,"result":{...}}
 *   {"jsonrpc":"2.0","id":<id>,"error":{"code":<int>,"message":"<str>"}}
 *
 * Supported modes:
 *   - Legacy script mode:
 *       node runner.js /path/to/eval.py
 *       or FUNCTION_PYODIDE_SCRIPT=/path/to/eval.py node runner.js
 *     The script must define evaluation_function(response, answer, params).
 *
 *   - Lambda Feedback package mode:
 *       FUNCTION_PYODIDE_ROOT=/path/to/evaluator/root \
 *       FUNCTION_PYODIDE_EVAL_ENTRYPOINT=evaluation_function.evaluation:evaluation_function \
 *       FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT=evaluation_function.preview:preview_function \
 *       FUNCTION_PYODIDE_ADAPTER=/path/to/lf_compat_adapter.py
 *     with no script arg.
 *     The evaluator package is mirrored into the Pyodide FS and loaded via
 *     lf_compat_adapter.call_function + normalize_result.
 */

const { loadPyodide } = require("pyodide");
const fs = require("fs");
const path = require("path");

const VFS_ROOT = "/__evaluator_root__";
const ADAPTER_VFS_ROOT = "/__lf_adapter_root__";
const ADAPTER_VFS_PATH = "/__lf_compat_adapter__.py";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const legacyScriptPath = process.argv[2] || process.env.FUNCTION_PYODIDE_SCRIPT;
const packageRootPath = process.env.FUNCTION_PYODIDE_ROOT;
const evalEntrypoint = process.env.FUNCTION_PYODIDE_EVAL_ENTRYPOINT;
const previewEntrypoint = process.env.FUNCTION_PYODIDE_PREVIEW_ENTRYPOINT;
const adapterPath = process.env.FUNCTION_PYODIDE_ADAPTER;

const packageMode = Boolean(packageRootPath && evalEntrypoint);
const legacyMode = Boolean(legacyScriptPath) && !packageMode;

function errorAndExit(message, code = 1) {
  process.stderr.write(`${message}\n`);
  process.exit(code);
}

function parsePackages(raw, defaultPackages = []) {
  const packages = [];

  if (raw) {
    for (const p of raw.split(",")) {
      const normalized = p.trim();
      if (normalized) packages.push(normalized);
    }
    return packages;
  }

  return defaultPackages.slice();
}

if (!legacyMode && !packageMode) {
  errorAndExit(
    "Usage: node runner.js <eval.py> " +
      "or set FUNCTION_PYODIDE_SCRIPT, or set FUNCTION_PYODIDE_ROOT + FUNCTION_PYODIDE_EVAL_ENTRYPOINT"
  );
}

let evalCode;
let resolvedScriptPath;
let resolvedRootPath;
let resolvedAdapterPath;

if (packageMode) {
  if (typeof evalEntrypoint !== "string" || evalEntrypoint.indexOf(":") === -1) {
    errorAndExit(
      "FUNCTION_PYODIDE_EVAL_ENTRYPOINT must be in module:function format"
    );
  }

  resolvedRootPath = path.resolve(packageRootPath);
  if (!fs.existsSync(resolvedRootPath)) {
    errorAndExit(`Package root not found: ${resolvedRootPath}`);
  }

  if (!adapterPath) {
    errorAndExit(
      "FUNCTION_PYODIDE_ADAPTER is required for package mode (e.g. examples/lambda-feedback-adapter/lf_compat_adapter.py)"
    );
  }

  resolvedAdapterPath = path.resolve(adapterPath);
  if (!fs.existsSync(resolvedAdapterPath)) {
    errorAndExit(`Lambda Feedback adapter not found: ${resolvedAdapterPath}`);
  }
} else {
  resolvedScriptPath = path.resolve(legacyScriptPath);
  if (!fs.existsSync(resolvedScriptPath)) {
    errorAndExit(`Script not found: ${resolvedScriptPath}`);
  }
  evalCode = fs.readFileSync(resolvedScriptPath, "utf8");
}

const defaultPackages = legacyMode ? ["scipy"] : [];
const pyodidePackages = parsePackages(process.env.FUNCTION_PYODIDE_PACKAGES, defaultPackages);

// ---------------------------------------------------------------------------
// LSP-framed stdio transport
// ---------------------------------------------------------------------------

/**
 * Write a JSON-RPC response to stdout, framed with Content-Length.
 */
function writeMessage(obj) {
  const body = JSON.stringify(obj);
  const header = `Content-Length: ${Buffer.byteLength(body, "utf8")}\r\n\r\n`;
  process.stdout.write(header + body);
}

/**
 * Build a JSON-RPC success response.
 */
function makeResult(id, result) {
  return { jsonrpc: "2.0", id, result };
}

/**
 * Build a JSON-RPC error response.
 */
function makeError(id, code, message, data) {
  const error = { code, message };
  if (data !== undefined) error.data = data;
  return { jsonrpc: "2.0", id, error };
}

// ---------------------------------------------------------------------------
// Framed-message reader
//
// The go-ethereum rpc library writes frames as:
//   Content-Length: N\r\n\r\n<N bytes>
// There may be stray output before the first Content-Length line (e.g. model
// loading logs), which we skip.
// ---------------------------------------------------------------------------

class FramedReader {
  constructor(stream) {
    this._stream = stream;
    this._buf = Buffer.alloc(0);
    this._resolvers = [];
    this._closed = false;

    stream.on("data", (chunk) => {
      this._buf = Buffer.concat([this._buf, chunk]);
      this._flush();
    });
    stream.on("end", () => {
      this._closed = true;
      for (const { reject } of this._resolvers) {
        reject(new Error("stdin closed"));
      }
      this._resolvers = [];
    });
    stream.on("error", (err) => {
      this._closed = true;
      for (const { reject } of this._resolvers) {
        reject(err);
      }
      this._resolvers = [];
    });
  }

  /** Return a promise that resolves with the next complete framed message. */
  read() {
    return new Promise((resolve, reject) => {
      this._resolvers.push({ resolve, reject });
      this._flush();
    });
  }

  _flush() {
    while (this._resolvers.length > 0) {
      const msg = this._tryParse();
      if (msg === null) break;
      const { resolve } = this._resolvers.shift();
      resolve(msg);
    }
  }

  /**
   * Try to extract one framed message from _buf.
   * Returns the message Buffer, or null if not enough data yet.
   */
  _tryParse() {
    let buf = this._buf;

    // Scan for "Content-Length:" line, skipping any stray output lines.
    let contentLength = -1;
    let searchPos = 0;

    while (true) {
      const nlIdx = buf.indexOf("\n", searchPos);
      if (nlIdx === -1) return null;

      const line = buf.slice(searchPos, nlIdx).toString("utf8").trimEnd();
      searchPos = nlIdx + 1;

      if (line.startsWith("Content-Length:")) {
        const parts = line.split(":", 2);
        contentLength = parseInt(parts[1].trim(), 10);
        if (isNaN(contentLength) || contentLength < 0) {
          contentLength = -1;
          continue;
        }
        break;
      }
      // Any other line: stray output, skip it.
    }

    if (contentLength < 0) return null;

    // Drain remaining header lines until blank separator (\r\n or \n).
    while (true) {
      const nlIdx = buf.indexOf("\n", searchPos);
      if (nlIdx === -1) return null;

      const line = buf.slice(searchPos, nlIdx).toString("utf8").trimEnd();
      searchPos = nlIdx + 1;

      if (line === "") break;
    }

    if (buf.length - searchPos < contentLength) return null;

    const body = buf.slice(searchPos, searchPos + contentLength);
    this._buf = buf.slice(searchPos + contentLength);
    return body;
  }
}

// ---------------------------------------------------------------------------
// Python bootstrap helpers
// ---------------------------------------------------------------------------

/**
 * Copy a host directory tree into Pyodide's virtual FS.
 */
function mirrorDirToPyodide(pyodide, sourcePath, targetPath) {
  const skip = new Set([".git", "__pycache__", ".venv", "node_modules"]);

  const walk = (src, dst) => {
    const dirEntries = fs.readdirSync(src, { withFileTypes: true });
    for (const dirent of dirEntries) {
      if (skip.has(dirent.name)) continue;

      const srcChild = path.join(src, dirent.name);
      const dstChild = path.join(dst, dirent.name);

      if (dirent.isDirectory()) {
        pyodide.FS.mkdirTree(dstChild);
        walk(srcChild, dstChild);
        continue;
      }

      if (dirent.isFile()) {
        const data = fs.readFileSync(srcChild);
        pyodide.FS.writeFile(dstChild, data);
        continue;
      }
    }
  };

  if (!fs.existsSync(sourcePath)) {
    throw new Error(`Source path does not exist: ${sourcePath}`);
  }

  if (!fs.statSync(sourcePath).isDirectory()) {
    throw new Error(`Source path is not a directory: ${sourcePath}`);
  }

  pyodide.FS.mkdirTree(targetPath);
  walk(sourcePath, targetPath);
}

/**
 * Normalize a Python result returned as a proxy.
 */
function asJs(resultProxy) {
  if (!resultProxy) return resultProxy;

  if (typeof resultProxy.toJs === "function") {
    const value = resultProxy.toJs({ dict_converter: Object.fromEntries });
    resultProxy.destroy();
    return value;
  }

  return resultProxy;
}

/**
 * Resolve request payload from JSON-RPC params.
 */
function requestPayload(request) {
  const { params } = request;
  const data = Array.isArray(params) ? params[0] : params;

  return {
    response: (data && data.response !== undefined ? data.response : null),
    answer: (data && data.answer !== undefined ? data.answer : null),
    params: (data && data.params !== undefined ? data.params : {}),
  };
}

/**
 * Configure package mode inside Pyodide: mount evaluator package and adapter.
 */
async function setupPackageMode(pyodide) {
  process.stderr.write(`Loading evaluator package from ${resolvedRootPath}\n`);
  mirrorDirToPyodide(pyodide, resolvedRootPath, VFS_ROOT);

  // Mirror the adapter directory too, not just lf_compat_adapter.py, because
  // real fixtures import the minimal lf_toolkit shim that lives next to the
  // adapter module.
  mirrorDirToPyodide(pyodide, path.dirname(resolvedAdapterPath), ADAPTER_VFS_ROOT);

  const adapterCode = fs.readFileSync(resolvedAdapterPath, "utf8");
  pyodide.FS.writeFile(ADAPTER_VFS_PATH, adapterCode);

  pyodide.globals.set("__eval_entrypoint__", evalEntrypoint);
  pyodide.globals.set("__preview_entrypoint__", previewEntrypoint || "");

  pyodide.runPython(
    `
import importlib
import importlib.util
import sys

# Make evaluator package modules importable.
if "${VFS_ROOT}" not in sys.path:
    sys.path.insert(0, "${VFS_ROOT}")

# Make the adapter's sibling lf_toolkit shim importable.
if "${ADAPTER_VFS_ROOT}" not in sys.path:
    sys.path.insert(0, "${ADAPTER_VFS_ROOT}")

# Keep common temp locations importable.
if "/tmp" not in sys.path:
    sys.path.insert(0, "/tmp")

# Make adapter available by loading source in the Pyodide FS.
spec = importlib.util.spec_from_file_location("lf_compat_adapter", "${ADAPTER_VFS_PATH}")
if spec is None or spec.loader is None:
    raise RuntimeError("Failed to build loader for lf_compat_adapter module")

lf_adapter = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = lf_adapter
spec.loader.exec_module(lf_adapter)

_eval_entrypoint = __eval_entrypoint__
_preview_entrypoint = __preview_entrypoint__

if not _eval_entrypoint:
    raise RuntimeError("FUNCTION_PYODIDE_EVAL_ENTRYPOINT is required in package mode")

_eval_fn = lf_adapter.load_entrypoint("${VFS_ROOT}", _eval_entrypoint)
_preview_fn = lf_adapter.load_entrypoint("${VFS_ROOT}", _preview_entrypoint) if _preview_entrypoint else None


def __lf_invoke(method, response, answer, params):
    if method == "preview" and _preview_fn is not None:
        fn = _preview_fn
    else:
        fn = _eval_fn

    if fn is None:
        raise RuntimeError("No evaluation function available")

    payload = {"response": response, "answer": answer, "params": params}
    normalized_method = "preview" if method == "preview" else "eval"
    return lf_adapter.call_function(
        fn,
        method=normalized_method,
        response=payload["response"],
        answer=payload["answer"],
        params=payload["params"],
    )
    `
  );
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  process.stderr.write("Loading Pyodide...\n");

  const pyodide = await loadPyodide();

  if (pyodidePackages.length > 0) {
    const packageList = pyodidePackages.join(", ");
    process.stderr.write(`Loading Pyodide packages: ${packageList}...\n`);
    await pyodide.loadPackage(pyodidePackages, {
      messageCallback: (msg) => process.stderr.write(msg + "\n"),
    });
  }

  if (legacyMode) {
    process.stderr.write(`Loading eval script: ${resolvedScriptPath}\n`);

    // Validate the script by running it once in a throw-away namespace.
    try {
      pyodide.runPython("exec(__eval_source__, {})", {
        globals: pyodide.toPy({ __eval_source__: evalCode }),
      });
    } catch (err) {
      process.stderr.write(`Error loading eval script: ${err}\n`);
      process.exit(1);
    }

    process.stderr.write("Ready.\n");
  } else {
    await setupPackageMode(pyodide);
    process.stderr.write("Ready.\n");
  }

  process.stdin.resume();
  const reader = new FramedReader(process.stdin);

  while (true) {
    let msgBuf;
    try {
      msgBuf = await reader.read();
    } catch (err) {
      break;
    }

    let request;
    try {
      request = JSON.parse(msgBuf.toString("utf8"));
    } catch (err) {
      writeMessage(makeError(null, -32700, "Parse error", err.message));
      continue;
    }

    const id = request.id;
    const method = request.method || "";

    if (method === "healthcheck") {
      writeMessage(makeResult(id, { status: "ok" }));
      continue;
    }

    const payload = requestPayload(request);

    try {
      const resolvedMethod = method === "preview" ? "preview" : "eval";
      const result = await handleRequest(pyodide, resolvedMethod, payload);
      writeMessage(makeResult(id, result));
    } catch (err) {
      writeMessage(makeError(id, -32603, String(err)));
      continue;
    }
  }
}

/**
 * Dispatch one JSON-RPC request to the Python eval function.
 */
async function handleRequest(pyodide, method, payload) {
  if (legacyMode) {
    // Legacy single-file mode: always call evaluation_function() for compatibility.
    const ns = pyodide.toPy({
      __eval_source__: evalCode,
      _response: payload.response,
      _answer: payload.answer,
      _params: payload.params,
    });

    try {
      const resultProxy = pyodide.runPython(
        `
# Fresh namespace — state isolation (no memory snapshot needed).
_ns = {}
exec(__eval_source__, _ns)

_fn = _ns.get("evaluation_function")
if _fn is None:
    raise RuntimeError("eval script does not define evaluation_function()")

_result = _fn(_response, _answer, _params)
_result
`,
        { globals: ns }
      );

      return asJs(resultProxy);
    } finally {
      ns.destroy();
    }
  }

  const paramsProxy = pyodide.toPy(payload.params ?? {});
  try {
    pyodide.globals.set("__method__", method);
    pyodide.globals.set("__response__", payload.response);
    pyodide.globals.set("__answer__", payload.answer);
    pyodide.globals.set("__params__", paramsProxy);

    const resultProxy = pyodide.runPython(
      `__lf_invoke(__method__, __response__, __answer__, __params__)`
    );
    return asJs(resultProxy);
  } finally {
    paramsProxy.destroy();
  }
}

main().catch((err) => {
  process.stderr.write(`Fatal: ${err}\n`);
  process.exit(1);
});
