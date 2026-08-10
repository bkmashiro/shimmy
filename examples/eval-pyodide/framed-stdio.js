"use strict";

const DEFAULT_MAX_FRAME_BYTES = 4 * 1024 * 1024;
const MAX_FRAME_HEADER_BYTES = 64 * 1024;

function encodeFrame(value, maxFrameBytes = DEFAULT_MAX_FRAME_BYTES) {
  const body = Buffer.isBuffer(value) ? value : Buffer.from(value, "utf8");
  if (body.length > maxFrameBytes) {
    throw new Error(
      `response frame exceeds maximum size: ${body.length} bytes > ${maxFrameBytes} bytes`
    );
  }
  return Buffer.concat([
    Buffer.from(`Content-Length: ${body.length}\r\n\r\n`, "ascii"),
    body,
  ]);
}

class FramedReader {
  constructor(
    stream,
    {
      maxFrameBytes = DEFAULT_MAX_FRAME_BYTES,
      maxHeaderBytes = MAX_FRAME_HEADER_BYTES,
    } = {}
  ) {
    if (!Number.isSafeInteger(maxFrameBytes) || maxFrameBytes < 0) {
      throw new Error("maxFrameBytes must be a non-negative safe integer");
    }
    if (!Number.isSafeInteger(maxHeaderBytes) || maxHeaderBytes <= 0) {
      throw new Error("maxHeaderBytes must be a positive safe integer");
    }

    this._stream = stream;
    this._maxFrameBytes = maxFrameBytes;
    this._maxHeaderBytes = maxHeaderBytes;
    this._buf = Buffer.alloc(0);
    this._resolvers = [];
    this._closed = false;
    this._fatalError = null;

    stream.on("data", (chunk) => {
      if (this._fatalError) return;
      this._buf = Buffer.concat([this._buf, chunk]);
      this._flush();
    });
    stream.on("end", () => {
      this._closed = true;
      if (this._resolvers.length > 0) {
        this._fail(new Error("stdin closed"));
      }
    });
    stream.on("error", (err) => {
      this._closed = true;
      this._fail(err);
    });
  }

  read() {
    if (this._fatalError) {
      return Promise.reject(this._fatalError);
    }
    if (this._closed && this._buf.length === 0) {
      return Promise.reject(new Error("stdin closed"));
    }

    return new Promise((resolve, reject) => {
      this._resolvers.push({ resolve, reject });
      this._flush();
    });
  }

  _fail(err) {
    if (!this._fatalError) this._fatalError = err;
    for (const { reject } of this._resolvers) {
      reject(this._fatalError);
    }
    this._resolvers = [];
    if (typeof this._stream.pause === "function") {
      this._stream.pause();
    }
  }

  _flush() {
    while (this._resolvers.length > 0 && !this._fatalError) {
      let msg;
      try {
        msg = this._tryParse();
      } catch (err) {
        this._fail(err);
        return;
      }
      if (msg === null) return;
      const { resolve } = this._resolvers.shift();
      resolve(msg);
    }
  }

  _tryParse() {
    const buf = this._buf;
    let contentLength = null;
    let searchPos = 0;

    while (true) {
      const nlIdx = buf.indexOf("\n", searchPos);
      if (nlIdx === -1) {
        if (buf.length > this._maxHeaderBytes) {
          throw new Error(
            `frame header exceeds maximum size of ${this._maxHeaderBytes} bytes`
          );
        }
        return null;
      }
      if (nlIdx + 1 > this._maxHeaderBytes) {
        throw new Error(
          `frame header exceeds maximum size of ${this._maxHeaderBytes} bytes`
        );
      }

      const line = buf.slice(searchPos, nlIdx).toString("utf8").trimEnd();
      searchPos = nlIdx + 1;

      if (line.startsWith("Content-Length") && !line.startsWith("Content-Length:")) {
        throw new Error("malformed Content-Length header");
      }
      if (!line.startsWith("Content-Length:")) {
        continue;
      }

      const rawLength = line.slice("Content-Length:".length).trim();
      if (!/^\d+$/.test(rawLength)) {
        throw new Error(`invalid Content-Length value: ${rawLength}`);
      }
      contentLength = Number(rawLength);
      if (!Number.isSafeInteger(contentLength)) {
        throw new Error(`invalid Content-Length value: ${rawLength}`);
      }
      if (contentLength > this._maxFrameBytes) {
        throw new Error(
          `frame exceeds maximum size: ${contentLength} bytes > ${this._maxFrameBytes} bytes`
        );
      }
      break;
    }

    while (true) {
      const nlIdx = buf.indexOf("\n", searchPos);
      if (nlIdx === -1) {
        if (buf.length > this._maxHeaderBytes) {
          throw new Error(
            `frame header exceeds maximum size of ${this._maxHeaderBytes} bytes`
          );
        }
        return null;
      }
      if (nlIdx + 1 > this._maxHeaderBytes) {
        throw new Error(
          `frame header exceeds maximum size of ${this._maxHeaderBytes} bytes`
        );
      }

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

module.exports = {
  DEFAULT_MAX_FRAME_BYTES,
  MAX_FRAME_HEADER_BYTES,
  FramedReader,
  encodeFrame,
};
