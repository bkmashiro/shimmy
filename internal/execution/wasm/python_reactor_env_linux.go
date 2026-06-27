//go:build linux

package wasm

import (
	"context"
	"encoding/binary"
	"math"
	"math/cmplx"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// ── float16 (IEEE 754 half-precision) helpers ────────────────────────────────

func float32ToHalf(f float32) uint16 {
	b := math.Float32bits(f)
	sign := uint16(b>>31) << 15
	exp := int((b>>23)&0xFF) - 127 + 15
	mant := b & 0x7FFFFF

	if exp <= 0 {
		if exp < -10 {
			return sign // underflow → ±0
		}
		// Subnormal: shift mantissa right
		mant = (mant | 0x800000) >> uint(1-exp)
		return sign | uint16(mant>>13)
	} else if exp >= 31 {
		return sign | 0x7C00 // overflow → ±Inf
	}
	return sign | uint16(exp)<<10 | uint16(mant>>13)
}

func halfToFloat32(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32((h >> 10) & 0x1F)
	mant := uint32(h & 0x3FF)

	if exp == 0 {
		if mant == 0 {
			return math.Float32frombits(sign) // ±0
		}
		// Subnormal: normalise
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3FF
	} else if exp == 31 {
		// Inf or NaN
		return math.Float32frombits(sign | 0x7F800000 | mant<<13)
	}
	return math.Float32frombits(sign | (exp+127-15)<<23 | mant<<13)
}

func float64ToHalf(f float64) uint16 { return float32ToHalf(float32(f)) }
func halfToFloat64(h uint16) float64 { return float64(halfToFloat32(h)) }

// float32BitsToHalf reinterprets the float32 bit-pattern as float16 bits
// (both stored as uint32/uint16 with sign|exp|mant layout, just different widths).
func float32BitsToHalf(bits32 uint32) uint16 {
	return float32ToHalf(math.Float32frombits(bits32))
}

func halfBitsToFloat32Bits(h uint16) uint32 {
	return math.Float32bits(halfToFloat32(h))
}

func float64BitsToHalf(bits64 uint64) uint16 {
	return float64ToHalf(math.Float64frombits(bits64))
}

func halfBitsToFloat64Bits(h uint16) uint64 {
	return math.Float64bits(halfToFloat64(h))
}

// half comparisons — convert to float32 then compare
func halfToF32(h uint32) float32 { return halfToFloat32(uint16(h)) }

func halfEq(a, b uint32) bool      { return halfToF32(a) == halfToF32(b) }
func halfNe(a, b uint32) bool      { return halfToF32(a) != halfToF32(b) }
func halfLt(a, b uint32) bool      { return halfToF32(a) < halfToF32(b) }
func halfLe(a, b uint32) bool      { return halfToF32(a) <= halfToF32(b) }
func halfGt(a, b uint32) bool      { return halfToF32(a) > halfToF32(b) }
func halfGe(a, b uint32) bool      { return halfToF32(a) >= halfToF32(b) }
func halfLtNoNaN(a, b uint32) bool { return halfToF32(a) < halfToF32(b) }

func halfIsNaN(h uint32) bool    { f := halfToF32(h); return f != f }
func halfIsInf(h uint32) bool    { return math.IsInf(float64(halfToF32(h)), 0) }
func halfIsFinite(h uint32) bool { f := halfToF32(h); return !math.IsInf(float64(f), 0) && f == f }
func halfIsZero(h uint32) bool   { return (uint16(h) & 0x7FFF) == 0 }
func halfSignbit(h uint32) bool  { return (h & 0x8000) != 0 }

func halfCopysign(x, y uint32) uint16 {
	return uint16(x&0x7FFF) | uint16(y&0x8000)
}

// halfSpacing returns the ULP spacing as a float16 bit-pattern.
func halfSpacing(h uint32) uint16 {
	f := halfToF32(h)
	if math.IsInf(float64(f), 0) || f != f {
		return 0x7E00 // NaN
	}
	next := float32(math.Nextafter(float64(f), math.Inf(1)))
	return float32ToHalf(next - f)
}

// halfNextafter returns the next float16 toward y.
func halfNextafter(xH, yH uint32) uint16 {
	x := float64(halfToF32(xH))
	y := float64(halfToF32(yH))
	return float64ToHalf(math.Nextafter(x, y))
}

// halfDivmod: compute quotient and remainder for float16 divmod.
// Writes the remainder into mem[remainderPtr] and returns the quotient bits.
func halfDivmod(xH, yH uint32, mem api.Memory, remainderPtr uint32) uint16 {
	x := float64(halfToF32(xH))
	y := float64(halfToF32(yH))
	if y == 0 {
		mem.WriteUint16Le(remainderPtr, float64ToHalf(math.NaN()))
		return float64ToHalf(math.NaN())
	}
	q := math.Floor(x / y)
	r := x - q*y
	mem.WriteUint16Le(remainderPtr, float64ToHalf(r))
	return float64ToHalf(q)
}

// ── complex memory helpers ───────────────────────────────────────────────────

func readComplex128(mem api.Memory, ptr uint32) complex128 {
	b, _ := mem.Read(ptr, 16)
	re := math.Float64frombits(binary.LittleEndian.Uint64(b[0:8]))
	im := math.Float64frombits(binary.LittleEndian.Uint64(b[8:16]))
	return complex(re, im)
}

func writeComplex128(mem api.Memory, ptr uint32, z complex128) {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[0:8], math.Float64bits(real(z)))
	binary.LittleEndian.PutUint64(b[8:16], math.Float64bits(imag(z)))
	mem.Write(ptr, b)
}

func readComplex64(mem api.Memory, ptr uint32) complex64 {
	b, _ := mem.Read(ptr, 8)
	re := math.Float32frombits(binary.LittleEndian.Uint32(b[0:4]))
	im := math.Float32frombits(binary.LittleEndian.Uint32(b[4:8]))
	return complex(re, im)
}

func writeComplex64(mem api.Memory, ptr uint32, z complex64) {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b[0:4], math.Float32bits(real(z)))
	binary.LittleEndian.PutUint32(b[4:8], math.Float32bits(imag(z)))
	mem.Write(ptr, b)
}

// complex64 wrappers for unary operations via complex128 math.
func unary64(mem api.Memory, dstPtr, srcPtr uint32, fn func(complex128) complex128) {
	z := complex128(readComplex64(mem, srcPtr))
	r := fn(z)
	writeComplex64(mem, dstPtr, complex64(r))
}

func unary128(mem api.Memory, dstPtr, srcPtr uint32, fn func(complex128) complex128) {
	z := readComplex128(mem, srcPtr)
	writeComplex128(mem, dstPtr, fn(z))
}

// ── npy_spacing helpers ──────────────────────────────────────────────────────

func npySpacing(x float64) float64 {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return math.NaN()
	}
	return math.Nextafter(x, math.Inf(1)) - x
}

func npySpacingF(x float32) float32 {
	if math.IsInf(float64(x), 0) || x != x {
		return float32(math.NaN())
	}
	next := float32(math.Nextafter(float64(x), math.Inf(1)))
	return next - x
}

// ── instantiateEnvModule ─────────────────────────────────────────────────────

// instantiateEnvModule registers the "env" module imports required by
// python-reactor.wasm (numpy, complex math, float16, random stubs, dl* stubs).
func instantiateEnvModule(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder("env")

	// ── Dynamic linking stubs (return NULL / 0) ───────────────────────────────

	// dlopen(filename i32, flags i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("dlopen")

	// dlsym(handle i32, symbol i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("dlsym")

	// dlerror() -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{}, []api.ValueType{api.ValueTypeI32}).
		Export("dlerror")

	// ── Float status (no-op / return 0) ──────────────────────────────────────

	// npy_clear_floatstatus_barrier(ptr i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_clear_floatstatus_barrier")

	// npy_get_floatstatus_barrier(ptr i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_get_floatstatus_barrier")

	// npy_set_floatstatus_divbyzero() -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
		}), []api.ValueType{}, []api.ValueType{}).
		Export("npy_set_floatstatus_divbyzero")

	// npy_set_floatstatus_invalid() -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
		}), []api.ValueType{}, []api.ValueType{}).
		Export("npy_set_floatstatus_invalid")

	// npy_set_floatstatus_overflow() -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
		}), []api.ValueType{}, []api.ValueType{}).
		Export("npy_set_floatstatus_overflow")

	// ── npy_expf ─────────────────────────────────────────────────────────────

	// npy_expf(x f32) -> f32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			x := math.Float32frombits(uint32(stack[0]))
			stack[0] = uint64(math.Float32bits(float32(math.Exp(float64(x)))))
		}), []api.ValueType{api.ValueTypeF32}, []api.ValueType{api.ValueTypeF32}).
		Export("npy_expf")

	// ── npy_spacing ───────────────────────────────────────────────────────────

	// npy_spacing(x f64) -> f64
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			x := math.Float64frombits(stack[0])
			stack[0] = math.Float64bits(npySpacing(x))
		}), []api.ValueType{api.ValueTypeF64}, []api.ValueType{api.ValueTypeF64}).
		Export("npy_spacing")

	// npy_spacingf(x f32) -> f32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			x := math.Float32frombits(uint32(stack[0]))
			stack[0] = uint64(math.Float32bits(npySpacingF(x)))
		}), []api.ValueType{api.ValueTypeF32}, []api.ValueType{api.ValueTypeF32}).
		Export("npy_spacingf")

	// npy_spacingl(dst i32, lohi i64, i64) -> void  (long double: treat as f64)
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			dst := uint32(stack[0])
			// Treat the first i64 as the f64 bit-pattern (little-endian long double)
			x := math.Float64frombits(stack[1])
			result := npySpacing(x)
			writeComplex128(mod.Memory(), dst, complex(result, 0)) // write 16 bytes (long double)
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI64, api.ValueTypeI64}, []api.ValueType{}).
		Export("npy_spacingl")

	// ── npy_cabs (absolute value of complex) ──────────────────────────────────

	// npy_cabs(ptr i32) -> f64   — complex128 at mem[ptr], returns |z|
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			z := readComplex128(mod.Memory(), uint32(stack[0]))
			stack[0] = math.Float64bits(cmplx.Abs(z))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeF64}).
		Export("npy_cabs")

	// npy_cabsf(ptr i32) -> f32   — complex64 at mem[ptr]
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			z := complex128(readComplex64(mod.Memory(), uint32(stack[0])))
			stack[0] = uint64(math.Float32bits(float32(cmplx.Abs(z))))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeF32}).
		Export("npy_cabsf")

	// npy_cabsl(dst i32, src i32) -> void  — complex long double (treat as f64)
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			z := readComplex128(mod.Memory(), uint32(stack[1]))
			result := cmplx.Abs(z)
			writeComplex128(mod.Memory(), uint32(stack[0]), complex(result, 0))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
		Export("npy_cabsl")

	// ── Unary complex functions (double, float, long double) ──────────────────
	// Pattern: npy_c<op>(dst i32, src i32) -> void

	type unaryComplexFn struct {
		name string
		fn   func(complex128) complex128
	}

	unaryOps := []unaryComplexFn{
		{"cacos", cmplx.Acos},
		{"cacosh", cmplx.Acosh},
		{"casin", cmplx.Asin},
		{"casinh", cmplx.Asinh},
		{"catan", cmplx.Atan},
		{"catanh", cmplx.Atanh},
		{"ccos", cmplx.Cos},
		{"ccosh", cmplx.Cosh},
		{"cexp", cmplx.Exp},
		{"clog", cmplx.Log},
		{"csin", cmplx.Sin},
		{"csinh", cmplx.Sinh},
		{"csqrt", cmplx.Sqrt},
		{"ctan", cmplx.Tan},
		{"ctanh", cmplx.Tanh},
	}

	for _, op := range unaryOps {
		op := op // capture loop variable

		// double version: npy_c<op>(dst i32, src i32) -> void
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
				unary128(mod.Memory(), uint32(stack[0]), uint32(stack[1]), op.fn)
			}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
			Export("npy_" + op.name)

		// float version: npy_c<op>f(dst i32, src i32) -> void
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
				unary64(mod.Memory(), uint32(stack[0]), uint32(stack[1]), op.fn)
			}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
			Export("npy_" + op.name + "f")

		// long double version: treat same as double
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
				unary128(mod.Memory(), uint32(stack[0]), uint32(stack[1]), op.fn)
			}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
			Export("npy_" + op.name + "l")
	}

	// ── cpow (3-arg: dst, base, exp) ─────────────────────────────────────────

	// npy_cpow(dst i32, base i32, exp i32) -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			base := readComplex128(mod.Memory(), uint32(stack[1]))
			exp := readComplex128(mod.Memory(), uint32(stack[2]))
			writeComplex128(mod.Memory(), uint32(stack[0]), cmplx.Pow(base, exp))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
		Export("npy_cpow")

	// npy_cpowf(dst i32, base i32, exp i32) -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			base := complex128(readComplex64(mod.Memory(), uint32(stack[1])))
			exp := complex128(readComplex64(mod.Memory(), uint32(stack[2])))
			writeComplex64(mod.Memory(), uint32(stack[0]), complex64(cmplx.Pow(base, exp)))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
		Export("npy_cpowf")

	// npy_cpowl(dst i32, base i32, exp i32) -> void  (long double = f64)
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			base := readComplex128(mod.Memory(), uint32(stack[1]))
			exp := readComplex128(mod.Memory(), uint32(stack[2]))
			writeComplex128(mod.Memory(), uint32(stack[0]), cmplx.Pow(base, exp))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{}).
		Export("npy_cpowl")

	// ── float16 / half-precision conversions ──────────────────────────────────

	// npy_float_to_half(f f32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			f := math.Float32frombits(uint32(stack[0]))
			stack[0] = uint64(float32ToHalf(f))
		}), []api.ValueType{api.ValueTypeF32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_float_to_half")

	// npy_double_to_half(f f64) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			f := math.Float64frombits(stack[0])
			stack[0] = uint64(float64ToHalf(f))
		}), []api.ValueType{api.ValueTypeF64}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_double_to_half")

	// npy_half_to_float(h i32) -> f32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			f := halfToFloat32(uint16(stack[0]))
			stack[0] = uint64(math.Float32bits(f))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeF32}).
		Export("npy_half_to_float")

	// npy_half_to_double(h i32) -> f64
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = math.Float64bits(halfToFloat64(uint16(stack[0])))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeF64}).
		Export("npy_half_to_double")

	// npy_floatbits_to_halfbits(bits i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(float32BitsToHalf(uint32(stack[0])))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_floatbits_to_halfbits")

	// npy_halfbits_to_floatbits(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(halfBitsToFloat32Bits(uint16(stack[0])))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_halfbits_to_floatbits")

	// npy_doublebits_to_halfbits(bits i64) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(float64BitsToHalf(stack[0]))
		}), []api.ValueType{api.ValueTypeI64}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_doublebits_to_halfbits")

	// npy_halfbits_to_doublebits(h i32) -> i64
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = halfBitsToFloat64Bits(uint16(stack[0]))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI64}).
		Export("npy_halfbits_to_doublebits")

	// ── float16 comparison functions ──────────────────────────────────────────

	// npy_half_eq(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfEq(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_eq")

	// npy_half_ne(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfNe(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_ne")

	// npy_half_lt(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfLt(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_lt")

	// npy_half_le(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfLe(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_le")

	// npy_half_gt(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfGt(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_gt")

	// npy_half_ge(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfGe(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_ge")

	// npy_half_lt_nonan(a i32, b i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfLtNoNaN(uint32(stack[0]), uint32(stack[1])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_lt_nonan")

	// ── float16 predicates ────────────────────────────────────────────────────

	// npy_half_isnan(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfIsNaN(uint32(stack[0])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_isnan")

	// npy_half_isinf(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfIsInf(uint32(stack[0])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_isinf")

	// npy_half_isfinite(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfIsFinite(uint32(stack[0])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_isfinite")

	// npy_half_iszero(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfIsZero(uint32(stack[0])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_iszero")

	// npy_half_signbit(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			if halfSignbit(uint32(stack[0])) {
				stack[0] = 1
			} else {
				stack[0] = 0
			}
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_signbit")

	// ── float16 arithmetic helpers ────────────────────────────────────────────

	// npy_half_copysign(x i32, y i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(halfCopysign(uint32(stack[0]), uint32(stack[1])))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_copysign")

	// npy_half_spacing(h i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(halfSpacing(uint32(stack[0])))
		}), []api.ValueType{api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_spacing")

	// npy_half_nextafter(x i32, y i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = uint64(halfNextafter(uint32(stack[0]), uint32(stack[1])))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_nextafter")

	// npy_half_divmod(x i32, y i32, remainderPtr i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, mod api.Module, stack []uint64) {
			q := halfDivmod(uint32(stack[0]), uint32(stack[1]), mod.Memory(), uint32(stack[2]))
			stack[0] = uint64(q)
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI32}).
		Export("npy_half_divmod")

	// ── numpy random hypergeometric (stubs — return 0) ────────────────────────
	// Full implementation requires rng state management; numpy.random is rarely
	// called in WASM context, so return 0 / write zeros.

	// random_hypergeometric(rng_state i32, good i64, bad i64, sample i64) -> i64
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI64, api.ValueTypeI64, api.ValueTypeI64}, []api.ValueType{api.ValueTypeI64}).
		Export("random_hypergeometric")

	// random_multivariate_hypergeometric_count(
	//   rng_state i32, total i64, n_colors i32, colors i32, nsample i64, out i32, steps i32) -> i32
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
			stack[0] = 0
		}), []api.ValueType{
			api.ValueTypeI32, api.ValueTypeI64, api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI64, api.ValueTypeI32, api.ValueTypeI32,
		}, []api.ValueType{api.ValueTypeI32}).
		Export("random_multivariate_hypergeometric_count")

	// random_multivariate_hypergeometric_marginals(
	//   rng_state i32, total i64, n_colors i32, colors i32, nsample i64, out i32, steps i32) -> void
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, _ api.Module, stack []uint64) {
		}), []api.ValueType{
			api.ValueTypeI32, api.ValueTypeI64, api.ValueTypeI32, api.ValueTypeI32,
			api.ValueTypeI64, api.ValueTypeI32, api.ValueTypeI32,
		}, []api.ValueType{}).
		Export("random_multivariate_hypergeometric_marginals")

	_, err := b.Instantiate(ctx)
	return err
}
