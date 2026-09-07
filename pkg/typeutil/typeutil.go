/*
Package typeutil detects nil through interfaces, obtains zero values generically,
dereferences pointers safely, and converts booleans to integers without a branch.

  - [IsNil]: reflection-based nil check that handles all nilable kinds (chan,
    func, interface, map, pointer, slice, unsafe pointer) and the untyped nil
    case, including a nil concrete pointer wrapped in a non-nil interface that
    `v == nil` misses.
  - [IsZero]: returns true when the value equals the zero value for its type
    (empty string, 0, nil pointer, false), without requiring a comparable
    constraint.
  - [Zero]: returns the zero value for any type T.
  - [Value]: dereferences a pointer, returning the zero value of T when the
    pointer is nil.
  - [BoolToInt]: converts a bool to 0 or 1 using the pattern the Go compiler
    optimizes to a single MOVBLZX instruction.

# Usage

	// Nil detection through an interface:
	var p *MyStruct
	var i any = p
	typeutil.IsNil(i) // true, whereas i == nil is false

	// Zero value inferred from an existing value, as a sentinel return:
	func check[T any](v T) (T, error) {
		if !valid(v) {
			return typeutil.Zero(v), errInvalid
		}
		return v, nil
	}

	// Safe pointer dereference:
	var timeout *time.Duration
	d := typeutil.Value(timeout) // 0, no panic

	// Branch-free bool-to-int:
	score += typeutil.BoolToInt(isBonus) * bonusPoints

# When To Use

  - Generic code must detect nil through an interface, where == nil is not
    enough.
  - You need the zero value of a type parameter.
*/
package typeutil

import (
	"reflect"
)

// IsNil reliably detects nil including nil pointers wrapped in non-nil interfaces; works on any nilable type.
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	value := reflect.ValueOf(v)

	// reflect.ValueOf unwraps the interface, so Kind is never Interface for a
	// top-level any argument; the Interface case is kept only for safety.
	switch value.Kind() { //nolint:exhaustive
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return value.IsNil()
	}

	return false
}

// IsZero returns true if value equals type T's zero value without requiring comparable constraint.
//
// It goes through reflection. The call is inlinable and normally allocates
// nothing, but if v (or its address) escapes at the call site it is
// heap-allocated; prefer a direct v == zero comparison on hot paths where T is a
// known comparable type.
func IsZero[T any](v T) bool {
	return reflect.ValueOf(&v).Elem().IsZero()
}

// Zero returns zero value for type T as a generic sentinel, useful for readable error returns.
//
// The argument exists only to infer T; it is evaluated but otherwise unused, so
// avoid passing an expensive or panic-prone expression.
func Zero[T any](_ T) T {
	var zero T
	return zero
}

// Value safely dereferences pointer p, returning zero value of T if nil.
//
// The nil check is a direct pointer comparison (no reflection), keeping this
// helper cheap on hot paths with frequent pointer dereferences.
func Value[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}

	return *p
}

// BoolToNum converts bool to 0 or 1 of any numeric type T, saving a cast at
// numeric call sites; see [BoolToInt] for the int-specific version.
//
// Integer instantiations keep the branch-free MOVBLZX codegen of [BoolToInt];
// float instantiations compile to a small conditional load.
func BoolToNum[T Number](b bool) T {
	var n T

	if b {
		n = 1
	} else {
		n = 0
	}

	return n
}

// BoolToInt converts bool to 0 or 1 with compiler optimization to MOVBLZX; avoids branch in hot paths.
//
// It delegates to [BoolToNum]; the int shape is inlined to the same single
// MOVBLZX instruction, so there is no cost over an open-coded version.
func BoolToInt(b bool) int {
	return BoolToNum[int](b)
}
