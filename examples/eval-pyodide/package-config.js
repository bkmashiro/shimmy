"use strict";

function parsePackages(raw, defaultPackages = []) {
  if (raw === undefined) return defaultPackages.slice();

  const packages = [];
  for (const value of raw.split(",")) {
    const normalized = value.trim();
    if (normalized) packages.push(normalized);
  }
  return packages;
}

module.exports = { parsePackages };
