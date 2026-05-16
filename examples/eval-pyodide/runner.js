"use strict";

/**
 * Pyodide/Node.js runner for shimmy eval functions.
 *
 * Protocol: go-ethereum JSON-RPC 2.0 over stdio, framed with LSP-style headers.
 *
 * Each message (both directions) is framed as:
 *   Content-Length: <N>\r\n
 *   \r\n
 *   <N bytes of JSON>
 *
 * Request JSON (from shimmy):
 *   {"jsonrpc":"2.0","id":<id>,"method":"<method>","params":[{...}]}
 *
 * Response JSON (to shimmy):
 *   {"jsonrpc":"2.0","id":<id>,"result":{...}}
 *   {"jsonrpc":"2.0","id":<id>,"error":{"code":<int>,"message":"<str>"}}
 *
 * Usage:
 *   node runner.js /path/to/eval.py
 *   FUNCTION_PYODIDE_SCRIPT=/path/to/eval.py node runner.js
 *
 * The eval Python script must define:
 *   evaluation_function(response, answer, params=None) -> dict
 *
 * State isolation: each request runs the eval function in a fresh Python
 * namespace via exec(code, {}). No memory snapshot is possible with Pyodide
 * JS-side state, so we rely on namespace isolation instead.
 */

const { loadPyodide } = require("pyodide");
const fs = require("fs");
const path = require("path");
const readline = require("readline");

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const scriptPath =
  process.argv[2] ||
  process.env.FUNCTION_PYODIDE_SCRIPT;

if (!scriptPath) {
  process.stderr.write(
    "Usage: node runner.js <eval.py> or set FUNCTION_PYODIDE_SCRIPT\n"
  );
  process.exit(1);
}

const resolvedScript = path.resolve(scriptPath);
if (!fs.existsSync(resolvedScript)) {
  process.stderr.write(`Script not found: ${resolvedScript}\n`);
  process.exit(1);
}

const evalCode = fs.readFileSync(resolvedScript, "utf8");

// ---------------------------------------------------------------------------
// LSP-framed stdio transport
// ---------------------------------------------------------------------------

/**
 * Write a JSON-RPC response to stdout, framed with Content-Length.
 * @param {object} obj
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
 * @param {number|string|null} id
 * @param {number} code  JSON-RPC error code (e.g. -32603 = internal error)
 * @param {string} message
 * @param {any} [data]
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
      if (nlIdx === -1) return null; // need more data

      const line = buf.slice(searchPos, nlIdx).toString("utf8").trimEnd();
      searchPos = nlIdx + 1;

      if (line.startsWith("Content-Length:")) {
        const parts = line.split(":", 2);
        contentLength = parseInt(parts[1].trim(), 10);
        if (isNaN(contentLength) || contentLength < 0) {
          // Malformed — skip this line and keep looking.
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

      if (line === "") break; // blank separator
    }

    // Check we have enough bytes for the body.
    if (buf.length - searchPos < contentLength) return null;

    const body = buf.slice(searchPos, searchPos + contentLength);
    this._buf = buf.slice(searchPos + contentLength);
    return body;
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  process.stderr.write("Loading Pyodide...\n");

  const pyodide = await loadPyodide();

  // Load micropip and install scipy (if available in the Pyodide package set).
  // In a real deployment you would pre-install packages at build time.
  await pyodide.loadPackage(["scipy"], { messageCallback: (msg) => process.stderr.write(msg + "\n") });

  process.stderr.write(`Loading eval script: ${resolvedScript}\n`);

  // Store the eval source in a Python variable so we can exec() it per-request
  // into a fresh namespace for state isolation.
  pyodide.globals.set("__eval_source__", evalCode);

  // Validate the script by running it once in a throw-away namespace.
  // This catches syntax errors at startup rather than on the first request.
  try {
    pyodide.runPython("exec(__eval_source__, {})");
  } catch (err) {
    process.stderr.write(`Error loading eval script: ${err}\n`);
    process.exit(1);
  }

  process.stderr.write("Ready.\n");

  // Switch stdin to raw binary mode so we can read arbitrary bytes.
  process.stdin.resume();

  const reader = new FramedReader(process.stdin);

  // Request loop.
  while (true) {
    let msgBuf;
    try {
      msgBuf = await reader.read();
    } catch (err) {
      // stdin closed or errored — exit cleanly.
      break;
    }

    let request;
    try {
      request = JSON.parse(msgBuf.toString("utf8"));
    } catch (err) {
      // Could not parse JSON — send parse error.
      writeMessage(makeError(null, -32700, "Parse error", err.message));
      continue;
    }

    const { id, method, params } = request;

    // params is an array; the single element is the data map from shimmy.
    // shimmy sends: rpcClient.CallContext(ctx, &result, method, data)
    // go-ethereum encodes positional args as a JSON array.
    const data = Array.isArray(params) ? params[0] : (params ?? {});

    let result;
    try {
      result = await handleRequest(pyodide, method, data);
    } catch (err) {
      writeMessage(makeError(id, -32603, String(err)));
      continue;
    }

    writeMessage(makeResult(id, result));
  }
}

/**
 * Dispatch one JSON-RPC request to the Python eval function.
 *
 * shimmy calls the method name as configured (typically "evaluate" or
 * "evaluation_function"). We always call evaluation_function() from the
 * loaded script regardless of method name, matching shimmy's convention.
 *
 * @param {object} pyodide
 * @param {string} method   JSON-RPC method name (e.g. "evaluate")
 * @param {object} data     Decoded params map from shimmy
 * @returns {Promise<object>} result map
 */
async function handleRequest(pyodide, method, data) {
  // Run the eval script in a fresh namespace for state isolation.
  // Then call evaluation_function with the request fields.
  const ns = pyodide.toPy({
    __eval_source__: evalCode,
    _response: data.response ?? null,
    _answer: data.answer ?? null,
    _params: data.params ?? {},
  });

  const resultProxy = pyodide.runPython(
    `
import json as _json

# Fresh namespace — state isolation (no memory snapshot needed).
_ns = {}
exec(__eval_source__, _ns)

_fn = _ns.get("evaluation_function")
if _fn is None:
    raise RuntimeError("eval script does not define evaluation_function()")

_result = _fn(_response, _answer, _params)

# Convert to a plain dict if the function returned a proxy object.
if hasattr(_result, "to_py"):
    _result = _result.to_py()
_result
`,
    { globals: ns }
  );

  // Convert the Pyodide proxy to a plain JS object.
  let result;
  if (resultProxy && typeof resultProxy.toJs === "function") {
    result = resultProxy.toJs({ dict_converter: Object.fromEntries });
    resultProxy.destroy();
  } else {
    result = resultProxy;
  }

  ns.destroy();
  return result;
}

main().catch((err) => {
  process.stderr.write(`Fatal: ${err}\n`);
  process.exit(1);
});
