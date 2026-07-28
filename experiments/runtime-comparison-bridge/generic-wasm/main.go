//go:build wasip1

package main

import (
	"encoding/binary"
	"encoding/json"
	"unsafe"

	"github.com/lambda-feedback/shimmy/experiments/runtime-comparison-bridge/contract"
)

var requestBuffer [256 * 1024]byte
var responseBuffer [256 * 1024]byte
var invocationCount uint64

type requestEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Response string            `json:"response"`
		Answer   string            `json:"answer"`
		Params   contract.Workload `json:"params"`
	} `json:"params"`
}

type responseEnvelope struct {
	Command string         `json:"command"`
	Result  any            `json:"result,omitempty"`
	Error   *responseError `json:"error,omitempty"`
}

type responseError struct {
	Message string `json:"message"`
}

//go:wasmexport alloc
func alloc(_ int32) int32 {
	return int32(uintptr(unsafe.Pointer(&requestBuffer[0])))
}

//go:wasmexport evaluate
func evaluate(_ int32, requestLength int32) int32 {
	var request requestEnvelope
	if err := json.Unmarshal(requestBuffer[:requestLength], &request); err != nil {
		writeResponse(responseEnvelope{Error: &responseError{Message: "decode request: " + err.Error()}})
		return int32(uintptr(unsafe.Pointer(&responseBuffer[0])))
	}
	if request.Method != "eval" {
		writeResponse(responseEnvelope{Error: &responseError{Message: "unsupported method: " + request.Method}})
		return int32(uintptr(unsafe.Pointer(&responseBuffer[0])))
	}

	invocationCount++
	result, err := contract.Evaluate(request.Params.Response, request.Params.Answer, request.Params.Params, invocationCount)
	if err != nil {
		writeResponse(responseEnvelope{Error: &responseError{Message: err.Error()}})
		return int32(uintptr(unsafe.Pointer(&responseBuffer[0])))
	}
	writeResponse(responseEnvelope{Command: "eval", Result: result})
	return int32(uintptr(unsafe.Pointer(&responseBuffer[0])))
}

func writeResponse(response responseEnvelope) {
	encoded, err := json.Marshal(response)
	if err != nil {
		encoded = []byte(`{"error":{"message":"encode response"}}`)
	}
	binary.LittleEndian.PutUint32(responseBuffer[:4], uint32(len(encoded)))
	copy(responseBuffer[4:], encoded)
}

func main() {}
