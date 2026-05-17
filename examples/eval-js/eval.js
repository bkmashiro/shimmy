/**
 * Example JavaScript evaluation function.
 *
 * This file contains the user-defined evaluation logic.  The runner (runner.js)
 * loads this file's source at startup and calls evaluationFunction() for every
 * incoming request, isolating each call in its own scope via Function().
 *
 * Contract
 * --------
 *   evaluationFunction(response, answer, params) → object
 *
 *   response  string | number   Student's answer
 *   answer    string | number   Reference answer
 *   params    object            Optional extra parameters (see below)
 *
 * Supported params keys
 * ---------------------
 *   tolerance   float    Absolute tolerance for numeric comparison (default 1e-6)
 *
 * Return value
 * ------------
 *   Must be a plain object with at least:
 *     is_correct  boolean
 *     feedback    string
 *   Any additional fields are returned verbatim to the HTTP caller.
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
