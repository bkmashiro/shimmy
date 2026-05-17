/**
 * Example JavaScript evaluation function for Shimmy-WASM (QuickJS/javy backend).
 *
 * This file contains the user-defined evaluation logic.  The runner (runner.js)
 * embeds this source at build time and executes it inside a fresh Function()
 * scope for every request, preventing global-variable mutations from leaking
 * between requests.
 *
 * Contract
 * --------
 *   evaluationFunction(response, answer, params) → object
 *     Called for "eval" requests.
 *     Must return an object with at least:
 *       is_correct  boolean
 *       feedback    string
 *
 *   previewFunction(response, answer, params) → object  [optional]
 *     Called for "preview" requests.
 *     If not defined, runner falls back to evaluationFunction.
 *     Should return a hint without revealing whether the answer is correct.
 *
 * Supported params keys
 * ---------------------
 *   tolerance   float    Absolute tolerance for numeric comparison (default 1e-6)
 */

"use strict";

function evaluationFunction(response, answer, params) {
  params = params || {};

  var tolerance = parseFloat(params.tolerance !== undefined ? params.tolerance : 1e-6);

  var r = parseFloat(response);
  var a = parseFloat(answer);

  if (isNaN(r) || isNaN(a)) {
    return {
      is_correct: false,
      feedback: "Could not parse values as numbers (response=" + response + ", answer=" + answer + ").",
    };
  }

  var absErr = Math.abs(r - a);
  var isCorrect = absErr <= tolerance;

  if (isCorrect) {
    return {
      is_correct: true,
      feedback: "Correct! " + r + " matches " + a + ".",
      absolute_error: absErr,
    };
  } else {
    return {
      is_correct: false,
      feedback:
        "Incorrect. Got " +
        r +
        ", expected " +
        a +
        ". Absolute error: " +
        absErr.toExponential(6) +
        " (tolerance: " +
        tolerance.toExponential(6) +
        ").",
      absolute_error: absErr,
    };
  }
}

function previewFunction(response, answer, params) {
  params = params || {};
  var tolerance = parseFloat(params.tolerance !== undefined ? params.tolerance : 1e-6);
  var r = parseFloat(response);

  if (isNaN(r)) {
    return {
      preview: "Could not parse response '" + response + "' as a number.",
    };
  }

  return {
    preview:
      "Your answer is " +
      r +
      ". The checker uses absolute tolerance " +
      tolerance.toExponential(2) +
      ".",
  };
}
