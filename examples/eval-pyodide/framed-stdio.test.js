"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { PassThrough } = require("node:stream");

const {
  DEFAULT_MAX_FRAME_BYTES,
  MAX_FRAME_HEADER_BYTES,
  FramedReader,
  encodeFrame,
} = require("./framed-stdio");

function readFrame(input, options) {
  const stream = new PassThrough();
  const reader = new FramedReader(stream, options);
  const result = reader.read();
  stream.end(input);
  return result;
}

test("uses the shared 4 MiB default", () => {
  assert.equal(DEFAULT_MAX_FRAME_BYTES, 4 * 1024 * 1024);
  assert.equal(MAX_FRAME_HEADER_BYTES, 64 * 1024);
});

test("encodes responses at the limit and rejects limit plus one", () => {
  assert.equal(
    encodeFrame("12345678", 8).toString("utf8"),
    "Content-Length: 8\r\n\r\n12345678"
  );
  assert.throws(() => encodeFrame("123456789", 8), /response frame exceeds/);
});

test("reads a valid fragmented frame", async () => {
  const stream = new PassThrough();
  const reader = new FramedReader(stream, { maxFrameBytes: 8 });
  const result = reader.read();
  stream.write("Content-Length: 5\r\n");
  stream.write("\r\nhe");
  stream.end("llo");
  assert.equal((await result).toString("utf8"), "hello");
});

test("allows a frame exactly at the configured limit", async () => {
  const body = "12345678";
  const result = await readFrame(`Content-Length: 8\r\n\r\n${body}`, {
    maxFrameBytes: 8,
  });
  assert.equal(result.toString("utf8"), body);
});

test("skips bounded stray output before Content-Length", async () => {
  const result = await readFrame(
    "loading evaluator...\nContent-Length: 2\r\n\r\nok",
    { maxFrameBytes: 8 }
  );
  assert.equal(result.toString("utf8"), "ok");
});

test("preserves a second complete frame buffered behind the first", async () => {
  const stream = new PassThrough();
  const reader = new FramedReader(stream, { maxFrameBytes: 8 });
  const first = reader.read();
  stream.end(
    "Content-Length: 3\r\n\r\noneContent-Length: 3\r\n\r\ntwo"
  );

  assert.equal((await first).toString("utf8"), "one");
  assert.equal((await reader.read()).toString("utf8"), "two");
});

test("rejects a negative Content-Length", async () => {
  await assert.rejects(
    readFrame("Content-Length: -1\r\n\r\n", { maxFrameBytes: 8 }),
    /invalid Content-Length/
  );
});

test("rejects a non-decimal Content-Length", async () => {
  await assert.rejects(
    readFrame("Content-Length: 1x\r\n\r\n", { maxFrameBytes: 8 }),
    /invalid Content-Length/
  );
});

test("rejects a frame above the configured limit before reading its body", async () => {
  await assert.rejects(
    readFrame("Content-Length: 9\r\n\r\n", { maxFrameBytes: 8 }),
    /frame exceeds maximum size/
  );
});

test("rejects an oversized header scan", async () => {
  await assert.rejects(
    readFrame(`${"x".repeat(MAX_FRAME_HEADER_BYTES + 1)}\n`, {
      maxFrameBytes: 8,
    }),
    /frame header exceeds maximum size/
  );
});
