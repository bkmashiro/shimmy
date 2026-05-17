"use strict";

/**
 * Shimmy JS evaluation function runner — compiled to WASM via javy.
 *
 * Architecture
 * ------------
 * This file is compiled into a standalone WebAssembly module by javy:
 *
 *   javy build -J javy-stream-io=y runner.js -o runner.wasm
 *
 * The resulting runner.wasm is run as a long-lived subprocess by shimmy's
 * RPC dispatcher (FUNCTION_INTERFACE=rpc, FUNCTION_RPC_TRANSPORT=stdio):
 *
 *   FUNCTION_INTERFACE=rpc \
 *   FUNCTION_RPC_TRANSPORT=stdio \
 *   FUNCTION_COMMAND="wazero run runner.wasm" \
 *     shimmy serve
 *
 * Wire protocol
 * -------------
 * shimmy speaks JSON-RPC 2.0 framed with LSP-style Content-Length headers
 * (same as the Pyodide runner):
 *
 *   Content-Length: <N>\r\n
 *   \r\n
 *   <N bytes of UTF-8 JSON>
 *
 * Both directions use the same framing.  The runner loops forever reading
 * framed requests from stdin and writing framed responses to stdout.
 *
 * Eval script isolation
 * ---------------------
 * The user's eval.js script is embedded as a string at build time (see the
 * EVAL_SOURCE constant below, which is replaced by the build step).  Each
 * request calls evaluationFunction() inside a fresh Function() scope so that
 * any global-variable mutations inside the user's code cannot leak between
 * requests — analogous to exec(source, {}) in the Python runner.
 *
 * Note: javy WASM modules are single-threaded; there is no concurrency inside
 * the module.  The shimmy dispatcher manages parallelism by spawning multiple
 * subprocess instances (one per pool slot).
 */

// ---------------------------------------------------------------------------
// Embedded eval source (replaced at build time by the build step)
// In production use "build-runner.sh <eval.js>" which patches this constant.
// ---------------------------------------------------------------------------

/*EVAL_SOURCE_BEGIN*/
var EVAL_SOURCE = (function () {
  // Default: load eval.js source embedded by the build script.
  // The build script replaces this block with the literal source.
  return null;
})();
/*EVAL_SOURCE_END*/

// ---------------------------------------------------------------------------
// WASI stream I/O constants
// ---------------------------------------------------------------------------

var STDIN = 0;
var STDOUT = 1;
var CHUNK_SIZE = 4096;

// ---------------------------------------------------------------------------
// Buffered stdin reader
// ---------------------------------------------------------------------------

var _readBuf = new Uint8Array(0);

/** Read more bytes from stdin into _readBuf. Returns false on EOF. */
function _refillBuffer() {
  var tmp = new Uint8Array(CHUNK_SIZE);
  var n = Javy.IO.readSync(STDIN, tmp);
  if (n <= 0) return false;
  var merged = new Uint8Array(_readBuf.length + n);
  merged.set(_readBuf);
  merged.set(tmp.subarray(0, n), _readBuf.length);
  _readBuf = merged;
  return true;
}

/**
 * Read one LSP-framed message from stdin.
 * Returns the UTF-8 body string, or null on EOF.
 */
function readMessage() {
  var dec = new TextDecoder();

  while (true) {
    var str = dec.decode(_readBuf);
    // Look for "Content-Length: N" followed by blank line separator.
    var match = str.match(/Content-Length:\s*(\d+)\r?\n\r?\n/);
    if (match) {
      var headerEnd = match.index + match[0].length;
      var bodyLen = parseInt(match[1], 10);
      var totalNeeded = headerEnd + bodyLen;
      if (_readBuf.length >= totalNeeded) {
        var body = _readBuf.subarray(headerEnd, totalNeeded);
        _readBuf = _readBuf.subarray(totalNeeded);
        return dec.decode(body);
      }
    }
    // Need more data from stdin.
    if (!_refillBuffer()) {
      return null; // EOF
    }
  }
}

/**
 * Write one LSP-framed message to stdout.
 * @param {object} obj  JSON-serialisable response object.
 */
function writeMessage(obj) {
  var enc = new TextEncoder();
  var body = JSON.stringify(obj);
  var bodyBytes = enc.encode(body);
  var header = "Content-Length: " + bodyBytes.length + "\r\n\r\n";
  var headerBytes = enc.encode(header);
  var frame = new Uint8Array(headerBytes.length + bodyBytes.length);
  frame.set(headerBytes);
  frame.set(bodyBytes, headerBytes.length);
  Javy.IO.writeSync(STDOUT, frame);
}

// ---------------------------------------------------------------------------
// Eval function dispatch
// ---------------------------------------------------------------------------

/**
 * Execute the user's evaluationFunction() in a fresh scope.
 *
 * Using Function() creates a new scope for every call, preventing any
 * variable mutations inside the user's script from persisting across requests.
 * This gives the same isolation guarantee as exec(source, {}) in Python.
 *
 * @param {string} source   Source code of eval.js (defines evaluationFunction)
 * @param {string} method   JSON-RPC method name (e.g. "evaluate", "eval")
 * @param {object} params   Decoded params from the request
 * @returns {object}        Result object to return to shimmy
 */
function dispatchEval(source, method, params) {
  // Build a wrapper that defines evaluationFunction from source, then calls it.
  var wrapper = new Function(
    "__response__",
    "__answer__",
    "__params__",
    source +
      "\n" +
      "return evaluationFunction(__response__, __answer__, __params__);"
  );

  var result = wrapper(
    params.response !== undefined ? params.response : null,
    params.answer !== undefined ? params.answer : null,
    params.params || {}
  );

  return result;
}

// ---------------------------------------------------------------------------
// JSON-RPC helpers
// ---------------------------------------------------------------------------

function makeResult(id, result) {
  return { jsonrpc: "2.0", id: id, result: result };
}

function makeError(id, code, message) {
  return { jsonrpc: "2.0", id: id, error: { code: code, message: message } };
}

// ---------------------------------------------------------------------------
// Main request loop
// ---------------------------------------------------------------------------

(function main() {
  if (EVAL_SOURCE === null) {
    // No eval source was embedded — this should not happen in production.
    // Write a fatal error to stderr and exit.
    Javy.IO.writeSync(
      2,
      new TextEncoder().encode(
        "runner.js: EVAL_SOURCE is null. " +
          "Run build-runner.sh to embed your eval.js before compiling with javy.\n"
      )
    );
    return;
  }

  // Validate the eval source once at startup (catches syntax errors early).
  try {
    new Function(EVAL_SOURCE);
  } catch (e) {
    Javy.IO.writeSync(
      2,
      new TextEncoder().encode("runner.js: eval.js syntax error: " + e + "\n")
    );
    return;
  }

  // Main request/response loop.
  while (true) {
    var msgStr = readMessage();
    if (msgStr === null) break; // stdin closed

    var req;
    try {
      req = JSON.parse(msgStr);
    } catch (e) {
      writeMessage(makeError(null, -32700, "Parse error: " + e));
      continue;
    }

    var id = req.id !== undefined ? req.id : null;
    var method = req.method || "";
    // shimmy sends params as a JSON array with one element (the data map).
    var paramsArr = Array.isArray(req.params) ? req.params : [req.params || {}];
    var data = paramsArr[0] || {};

    try {
      var result;
      if (method === "healthcheck") {
        result = { status: "ok" };
      } else {
        // "eval", "evaluate", "preview" — all dispatch to evaluationFunction.
        result = dispatchEval(EVAL_SOURCE, method, data);
      }
      writeMessage(makeResult(id, result));
    } catch (e) {
      writeMessage(makeError(id, -32603, String(e)));
    }
  }
})();
