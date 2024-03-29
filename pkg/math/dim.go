// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

// Dim returns the maximum of x-y or 0.
//
// Special cases are:
//	Dim(+Inf, +Inf) = NaN
//	Dim(-Inf, -Inf) = NaN
//	Dim(x, NaN) = Dim(NaN, x) = NaN
func Dim(x, y float64) float64

func dim(x, y float64) float64 {
	return max(x-y, 0)
}

// Max returns the larger of x or y.
//
// Special cases are:
//	Max(x, +Inf) = Max(+Inf, x) = +Inf
//	Max(x, NaN) = Max(NaN, x) = NaN
//	Max(+0, ±0) = Max(±0, +0) = +0
//	Max(-0, -0) = -0
func Max(x, y float64) float64

func max(x, y float64) float64 {
	// TODO(rsc): Remove manual inlining of IsNaN, IsInf
	// when compiler does it for us
	// special cases
	switch {
	case x > MaxFloat64 || y > MaxFloat64: // IsInf(x, 1) || IsInf(y, 1):
		return Inf(1)
	case x != x || y != y: // IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return y
		}
		return x
	}
	if x > y {
		return x
	}
	return y
}

// Min returns the smaller of x or y.
//
// Special cases are:
//	Min(x, -Inf) = Min(-Inf, x) = -Inf
//	Min(x, NaN) = Min(NaN, x) = NaN
//	Min(-0, ±0) = Min(±0, -0) = -0
func Min(x, y float64) float64

func min(x, y float64) float64 {
	// TODO(rsc): Remove manual inlining of IsNaN, IsInf
	// when compiler does it for us
	// special cases
	switch {
	case x < -MaxFloat64 || y < -MaxFloat64: // IsInf(x, -1) || IsInf(y, -1):
		return Inf(-1)
	case x != x || y != y: // IsNaN(x) || IsNaN(y):
		return NaN()
	case x == 0 && x == y:
		if Signbit(x) {
			return x
		}
		return y
	}
	if x < y {
		return x
	}
	return y
}
