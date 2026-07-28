"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { parsePackages } = require("./package-config");

test("uses defaults only when the environment value is absent", () => {
  assert.deepEqual(parsePackages(undefined, ["scipy"]), ["scipy"]);
  assert.deepEqual(parsePackages("", ["scipy"]), []);
});

test("normalizes an explicit comma-separated package list", () => {
  assert.deepEqual(parsePackages(" numpy, scipy ,, ", []), ["numpy", "scipy"]);
});
