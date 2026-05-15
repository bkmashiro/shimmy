//go:build wasip1

package main

import (
	"encoding/binary"
	"encoding/json"
	"unsafe"
)

// Static buffers — single-threaded WASM, one request at a time.
var reqBuf [256 * 1024]byte
var respBuf [256 * 1024]byte

// alloc is called by the host to get a pointer where it will write the request.
// We always return the start of reqBuf (one request at a time).
//
//go:wasmexport alloc
func alloc(size int32) int32 {
	_ = size
	return int32(uintptr(unsafe.Pointer(&reqBuf[0])))
}

// evaluate reads the JSON request from reqBuf, processes it, and writes
// a length-prefixed JSON response to respBuf.
// Returns a pointer to respBuf[0] (4-byte LE length + JSON body).
//
//go:wasmexport evaluate
func evaluate(reqPtr int32, reqLen int32) int32 {
	_ = reqPtr // we know it's &reqBuf[0]

	// Parse request envelope: {"method": "...", "params": {...}}
	type Request struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}

	var req Request
	if err := json.Unmarshal(reqBuf[:reqLen], &req); err != nil {
		writeResp(map[string]any{"error": map[string]any{"message": err.Error()}})
		return int32(uintptr(unsafe.Pointer(&respBuf[0])))
	}

	// Simple echo eval: always mark as correct, echo back params as feedback.
	// Response must match the response-eval.json schema:
	//   {"command": "eval", "result": {"is_correct": bool, ...}}
	resp := map[string]any{
		"command": req.Method,
		"result": map[string]any{
			"is_correct": true,
			"feedback":   req.Params,
		},
	}

	writeResp(resp)
	return int32(uintptr(unsafe.Pointer(&respBuf[0])))
}

func writeResp(v map[string]any) {
	data, err := json.Marshal(v)
	if err != nil {
		data = []byte(`{"error":{"message":"marshal failed"}}`)
	}
	binary.LittleEndian.PutUint32(respBuf[:4], uint32(len(data)))
	copy(respBuf[4:], data)
}

func main() {}
