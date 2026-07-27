package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

//go:embed fixtures/evaluator.py.tmpl
var evaluatorTemplate string

const (
	PayloadShapeFlatASCII    = "flat-ascii"
	PayloadShapeNested       = "nested"
	PayloadShapeNumericArray = "numeric-array"
	PayloadShapeUTF8         = "utf8"
)

var (
	InputPayloadTargets  = []int{256, 1024, 4096, 16384, 65536, 262144, 524288, 1044480}
	OutputPayloadTargets = []int{128, 4096, 65536, 262144, 524288, 921600}
)

var PayloadShapeValues = []string{PayloadShapeFlatASCII, PayloadShapeNested, PayloadShapeNumericArray, PayloadShapeUTF8}

const (
	maxInputPayloadBytes  = 1044480
	maxOutputPayloadBytes = 921600
)

var dirtyBPSValues = []int{0, 1, 10, 100, 1000, 5000, 10000}
var arenaMiBValues = []int{0, 8, 32, 64, 128}
var dirtyPagePatterns = []string{"contiguous", "sparse", "fixed-seed-random"}

// EvaluatorTemplateData controls deterministic generation of evaluator template text.
type EvaluatorTemplateData struct {
	ArenaMiB   int
	Sentinel   int
	Seed       int64
	CPUMode    string
	CPUProfile string
}

func BuildSyntheticPayload(target int, shape string, seed int64) ([]byte, error) {
	if target < 2 {
		return nil, fmt.Errorf("target %d too small", target)
	}
	if target > MaxPayloadBytes {
		return nil, fmt.Errorf("payload target %d exceeds hard max %d", target, MaxPayloadBytes)
	}

	switch shape {
	case PayloadShapeFlatASCII:
		return buildFlatPayload(target)
	case PayloadShapeNested:
		return buildNestedPayload(target)
	case PayloadShapeNumericArray:
		return buildNumericArrayPayload(target)
	case PayloadShapeUTF8:
		return buildUTF8Payload(target)
	default:
		return nil, fmt.Errorf("unsupported payload shape %q", shape)
	}
}

func buildFlatPayload(target int) ([]byte, error) {
	base := struct {
		Kind    string `json:"kind"`
		Payload string `json:"payload"`
	}{
		Kind:    PayloadShapeFlatASCII,
		Payload: "",
	}
	baseLen := len(mustJSON(base))
	fill := target - baseLen
	if fill < 0 {
		return nil, fmt.Errorf("target too small for flat-ascii")
	}
	payload := mustJSON(map[string]any{"kind": PayloadShapeFlatASCII, "payload": strings.Repeat("A", fill)})
	return payload, nil
}

func buildUTF8Payload(target int) ([]byte, error) {
	base := struct {
		Kind    string `json:"kind"`
		Payload string `json:"payload"`
	}{
		Kind:    PayloadShapeUTF8,
		Payload: "",
	}
	baseLen := len(mustJSON(base))
	fill := target - baseLen
	if fill < 0 {
		return nil, fmt.Errorf("target too small for utf8")
	}
	runeUnit := len("界")
	utf8Count := fill / runeUnit
	asciiCount := fill % runeUnit
	if asciiCount > 2 {
		utf8Count--
		asciiCount += runeUnit
	}
	if utf8Count < 0 {
		return nil, fmt.Errorf("target cannot be represented by shape %s", PayloadShapeUTF8)
	}
	filler := strings.Repeat("界", utf8Count) + strings.Repeat("A", asciiCount)
	payload := mustJSON(map[string]any{"kind": PayloadShapeUTF8, "payload": filler})
	if len(payload) != target {
		return nil, fmt.Errorf("utf8 payload mismatch target=%d actual=%d", target, len(payload))
	}
	return payload, nil
}

func buildNestedPayload(target int) ([]byte, error) {
	base := nestedBody("")
	baseLen := len(base)
	fill := target - baseLen
	if fill < 0 {
		return nil, fmt.Errorf("target too small for nested")
	}
	payload := nestedBody(strings.Repeat("A", fill))
	if len(payload) != target {
		return nil, fmt.Errorf("nested payload mismatch target=%d actual=%d", target, len(payload))
	}
	return payload, nil
}

func nestedBody(payload string) []byte {
	leaf := map[string]any{"leaf": payload}
	node := leaf
	for i := 0; i < 6; i++ {
		node = map[string]any{
			fmt.Sprintf("n%d", i): node,
		}
	}
	obj := map[string]any{"kind": PayloadShapeNested, "payload": node}
	b, _ := json.Marshal(obj)
	return b
}

func buildNumericArrayPayload(target int) ([]byte, error) {
	prefix := `{"kind":"` + PayloadShapeNumericArray + `","values":[`
	suffix := `]}`
	for n := 1; n <= target; n++ {
		// values use n-1 times "1" plus last variable-size digit token.
		lastLen := target - (len(prefix) + len(suffix) + (2*n - 2))
		if lastLen < 1 || lastLen > 12 {
			continue
		}
		parts := make([]string, 0, n)
		for i := 0; i < n-1; i++ {
			parts = append(parts, "1")
		}
		parts = append(parts, strings.Repeat("0", lastLen))
		b := append([]byte(prefix), []byte(strings.Join(parts, ","))...)
		b = append(b, suffix...)
		if len(b) == target {
			return b, nil
		}
	}
	return nil, fmt.Errorf("unable to satisfy numeric-array target %d", target)
}

func DirtyPages(arenaMiB int, dirtyBps int, pattern string, seed int64) ([]int, error) {
	if dirtyBps < 0 || dirtyBps > 10000 {
		return nil, fmt.Errorf("dirty bps %d outside [0,10000]", dirtyBps)
	}
	if arenaMiB < 0 {
		return nil, fmt.Errorf("negative arena")
	}
	if !containsString(dirtyPagePatterns, pattern) {
		return nil, fmt.Errorf("unsupported dirty pattern %q", pattern)
	}
	pages := arenaMiB * 1024 / 4
	if pages <= 0 {
		return nil, nil
	}
	if dirtyBps == 0 {
		return nil, nil
	}
	count := (pages*dirtyBps + 9999) / 10000
	if count > pages {
		count = pages
	}
	if count < 1 {
		count = 1
	}

	switch pattern {
	case "contiguous":
		idx := make([]int, 0, count)
		for i := 0; i < count; i++ {
			idx = append(idx, i)
		}
		return idx, nil
	case "sparse":
		idx := make([]int, 0, count)
		if count >= pages {
			for i := 0; i < pages; i++ {
				idx = append(idx, i)
			}
			return idx, nil
		}
		step := int(math.Ceil(float64(pages) / float64(count)))
		for i := 0; i < count; i++ {
			idx = append(idx, i*step)
		}
		return idx[:count], nil
	case "fixed-seed-random":
		perm := make([]int, pages)
		for i := 0; i < pages; i++ {
			perm[i] = i
		}
		r := rand.New(rand.NewSource(seed))
		r.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		return perm[:count], nil
	default:
		return nil, fmt.Errorf("unsupported dirty pattern %q", pattern)
	}
}

func MustStablePagesChecksum(payload []byte) string {
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:8])
}

type CPUSummary struct {
	Profile string `json:"profile"`
	Iter    int    `json:"iterations"`
}

var KnownCPUProfiles = map[string]CPUSummary{
	"none":            {Profile: "none", Iter: 0},
	"python-10k":      {Profile: "python-10k", Iter: 10_000},
	"python-100k":     {Profile: "python-100k", Iter: 100_000},
	"python-1m":       {Profile: "python-1m", Iter: 1_000_000},
	"python-5m":       {Profile: "python-5m", Iter: 5_000_000},
	"numpy-16k":       {Profile: "numpy-16k", Iter: 16_000},
	"numpy-256k":      {Profile: "numpy-256k", Iter: 256_000},
	"numpy-1m":        {Profile: "numpy-1m", Iter: 1_000_000},
	"numpy-matmul-64": {Profile: "numpy-matmul-64", Iter: 64 * 64},
}

func CPUChecksum(profile string, seed int64) (string, int, error) {
	p, ok := KnownCPUProfiles[profile]
	if !ok {
		return "", 0, fmt.Errorf("unknown CPU profile %q", profile)
	}
	payload := []byte(fmt.Sprintf("%s:%d:%d", profile, p.Iter, seed))
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:8]), p.Iter, nil
}

func RenderEvaluatorTemplate(data EvaluatorTemplateData) (string, error) {
	if data.ArenaMiB <= 0 {
		data.ArenaMiB = 0
	}
	if data.Sentinel == 0 {
		data.Sentinel = 0xd3
	}
	var b bytes.Buffer
	t := template.Must(template.New("fixture").Parse(evaluatorTemplate))
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

func BuildInputPayloadByShape(shape string, target int) ([]byte, error) {
	if target > maxInputPayloadBytes {
		return nil, fmt.Errorf("input target %d exceeds input max %d", target, maxInputPayloadBytes)
	}
	return BuildSyntheticPayload(target, shape, 0)
}

func BuildOutputPayloadByShape(shape string, target int) ([]byte, error) {
	if target > maxOutputPayloadBytes {
		return nil, fmt.Errorf("output target %d exceeds output max %d", target, maxOutputPayloadBytes)
	}
	return BuildSyntheticPayload(target, shape, 0)
}

func ArenaPagesFromMiB(arenaMiB int) int {
	if arenaMiB <= 0 {
		return 0
	}
	return arenaMiB * 256
}

func SyntheticFixtureDigest(payload []byte) string {
	h := sha256.Sum256(payload)
	return strings.ToUpper(hex.EncodeToString(h[:]))[:16]
}

func BoundSyntheticPayloadByLength(target int, seed int64) ([]byte, string, error) {
	err := error(nil)
	for _, shape := range PayloadShapeValues {
		b, e := BuildSyntheticPayload(target, shape, seed)
		if e == nil {
			if len(b) == target {
				return b, shape, nil
			}
		}
		err = e
	}
	if err == nil {
		err = fmt.Errorf("no shape matches target %d", target)
	}
	return nil, "", err
}

func ValidatePayloadTargets() error {
	seen := map[int]struct{}{}
	for _, t := range InputPayloadTargets {
		if _, ok := seen[t]; ok {
			return fmt.Errorf("duplicate input target %d", t)
		}
		seen[t] = struct{}{}
		if t > maxInputPayloadBytes {
			return fmt.Errorf("input target too high: %d", t)
		}
	}
	for _, t := range OutputPayloadTargets {
		if t > maxOutputPayloadBytes {
			return fmt.Errorf("output target too high: %d", t)
		}
	}
	return nil
}

func sortIntSlice(values []int) string {
	copyv := append([]int{}, values...)
	sort.Ints(copyv)
	parts := make([]string, 0, len(copyv))
	for _, v := range copyv {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ",")
}
