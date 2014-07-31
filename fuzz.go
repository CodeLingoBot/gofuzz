/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fuzz

import (
	"fmt"
	"math/rand"
	"reflect"
	"time"
)

// fuzzFuncMap is a map from a type to a fuzzFunc that handles that type.
type fuzzFuncMap map[reflect.Type]reflect.Value

// Fuzzer knows how to fill any object with random fields.
type Fuzzer struct {
	fuzzFuncs   fuzzFuncMap
	r           *rand.Rand
	nilChance   float64
	minElements int
	maxElements int
}

// New returns a new Fuzzer. with the given custom fuzzing functions.
//
//
// TODO: Make probability of getting a nil customizable.
func New(fuzzFuncs ...interface{}) *Fuzzer {
	f := &Fuzzer{
		fuzzFuncs:   fuzzFuncMap{},
		r:           rand.New(rand.NewSource(time.Now().UnixNano())),
		nilChance:   .2,
		minElements: 1,
		maxElements: 10,
	}
	return f
}

// Each entry in fuzzFuncs must be a function taking two parameters.
// The first parameter must be a pointer or map. It is the variable that
// function will fill with random data. The second parameter must be a
// fuzz.Continue, which will provide a source of randomness and a way
// to automatically continue fuzzing smaller pieces of the first parameter.
//
// These functions are called sensibly, e.g., if you wanted custom string
// fuzzing, the function `func(s *string, c fuzz.Continue)` would get
// called and passed the address of strings. Maps and pointers will always
// be made/new'd for you, ignoring the NilChange option. For slices, it
// doesn't make much sense to  pre-create them--Fuzzer doesn't know how
// long you want your slice--so take a pointer to a slice, and make it
// yourself. (If you don't want your map/pointer type pre-made, take a
// pointer to it, and make it yourself.) See the examples for a range of
// custom functions.
func (f *Fuzzer) Funcs(fuzzFuncs ...interface{}) *Fuzzer {
	for i := range fuzzFuncs {
		v := reflect.ValueOf(fuzzFuncs[i])
		if v.Kind() != reflect.Func {
			panic("Need only funcs!")
		}
		t := v.Type()
		if t.NumIn() != 2 || t.NumOut() != 0 {
			panic("Need 2 in and 0 out params!")
		}
		argT := t.In(0)
		switch argT.Kind() {
		case reflect.Ptr, reflect.Map:
		default:
			panic("fuzzFunc must take pointer or map type")
		}
		if t.In(1) != reflect.TypeOf(Continue{}) {
			panic("fuzzFunc's second parameter must be type fuzz.Continue")
		}
		f.fuzzFuncs[argT] = v
	}
	return f
}

// SetRandSource causes f to get values from the given source of randomness.
// Use if you want deterministic fuzzing.
func (f *Fuzzer) RandSource(s rand.Source) *Fuzzer {
	f.r = rand.New(s)
	return f
}

// NilChance sets the probability of creating a nil pointer, map, or slice to
// 'p'. 'p' should be between 0 (no nils) and 1 (all nils), inclusive.
func (f *Fuzzer) NilChance(p float64) *Fuzzer {
	if p < 0 || p > 1 {
		panic("p should be between 0 and 1, inclusive.")
	}
	f.nilChance = p
	return f
}

// NumElements sets the minimum and maximum number of elements that will be
// added to a non-nil map or slice.
func (f *Fuzzer) NumElements(atLeast, atMost int) *Fuzzer {
	if atLeast > atMost {
		panic("atLeast must be <= atMost")
	}
	if atLeast < 0 {
		panic("atLeast must be >= 0")
	}
	f.minElements = atLeast
	f.maxElements = atMost
	return f
}

func (f *Fuzzer) genElementCount() int {
	if f.minElements == f.maxElements {
		return f.minElements
	}
	return f.minElements + f.r.Intn(f.maxElements-f.minElements)
}

func (f *Fuzzer) genShouldFill() bool {
	return f.r.Float64() > f.nilChance
}

// Fuzz recursively fills all of obj's fields with something random.
// Not safe for cyclic or tree-like structs!
// obj must be a pointer. Only exported (public) fields can be set (thanks, golang :/ )
// Intended for tests, so will panic on bad input or unimplemented fields.
func (f *Fuzzer) Fuzz(obj interface{}) {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Ptr {
		panic("needed ptr!")
	}
	v = v.Elem()
	f.doFuzz(v)
}

func (f *Fuzzer) doFuzz(v reflect.Value) {
	if !v.CanSet() {
		return
	}
	// Check for both pointer and non-pointer custom functions.
	if v.CanAddr() && f.tryCustom(v.Addr()) {
		return
	}
	if f.tryCustom(v) {
		return
	}
	if fn, ok := fillFuncMap[v.Kind()]; ok {
		fn(v, f.r)
		return
	}
	switch v.Kind() {
	case reflect.Map:
		if f.genShouldFill() {
			v.Set(reflect.MakeMap(v.Type()))
			n := f.genElementCount()
			for i := 0; i < n; i++ {
				key := reflect.New(v.Type().Key()).Elem()
				f.doFuzz(key)
				val := reflect.New(v.Type().Elem()).Elem()
				f.doFuzz(val)
				v.SetMapIndex(key, val)
			}
			return
		}
		v.Set(reflect.Zero(v.Type()))
	case reflect.Ptr:
		if f.genShouldFill() {
			v.Set(reflect.New(v.Type().Elem()))
			f.doFuzz(v.Elem())
			return
		}
		v.Set(reflect.Zero(v.Type()))
	case reflect.Slice:
		if f.genShouldFill() {
			n := f.genElementCount()
			v.Set(reflect.MakeSlice(v.Type(), n, n))
			for i := 0; i < n; i++ {
				f.doFuzz(v.Index(i))
			}
			return
		}
		v.Set(reflect.Zero(v.Type()))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f.doFuzz(v.Field(i))
		}
	case reflect.Array:
		fallthrough
	case reflect.Chan:
		fallthrough
	case reflect.Func:
		fallthrough
	case reflect.Interface:
		fallthrough
	default:
		panic(fmt.Sprintf("Can't handle %#v", v.Interface()))
	}
}

// tryCustom searches for custom handlers, and returns true iff it finds a match
// and successfully randomizes v.
func (f *Fuzzer) tryCustom(v reflect.Value) bool {
	doCustom, ok := f.fuzzFuncs[v.Type()]
	if !ok {
		return false
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			if !v.CanSet() {
				return false
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
	case reflect.Map:
		if v.IsNil() {
			if !v.CanSet() {
				return false
			}
			v.Set(reflect.MakeMap(v.Type()))
		}
	default:
		return false
	}

	doCustom.Call([]reflect.Value{v, reflect.ValueOf(Continue{f})})
	return true
}

// Continue can be passed to custom fuzzing functions to allow them to use
// the correct source of randomness and to continue fuzzing their members.
type Continue struct {
	f *Fuzzer
}

// Fuzz continues fuzzing obj. obj must be a pointer.
func (c Continue) Fuzz(obj interface{}) {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Ptr {
		panic("needed ptr!")
	}
	v = v.Elem()
	c.f.doFuzz(v)
}

// Rand returns a *rand.Rand which you should get all random values from--
// otherwise, the fuzzing won't be repeatable from a given seed.
func (c Continue) Rand() *rand.Rand {
	return c.f.r
}

func fuzzInt(v reflect.Value, r *rand.Rand) {
	v.SetInt(int64(RandUint64(r)))
}

func fuzzUint(v reflect.Value, r *rand.Rand) {
	v.SetUint(RandUint64(r))
}

var fillFuncMap = map[reflect.Kind]func(reflect.Value, *rand.Rand){
	reflect.Bool: func(v reflect.Value, r *rand.Rand) {
		v.SetBool(RandBool(r))
	},
	reflect.Int:     fuzzInt,
	reflect.Int8:    fuzzInt,
	reflect.Int16:   fuzzInt,
	reflect.Int32:   fuzzInt,
	reflect.Int64:   fuzzInt,
	reflect.Uint:    fuzzUint,
	reflect.Uint8:   fuzzUint,
	reflect.Uint16:  fuzzUint,
	reflect.Uint32:  fuzzUint,
	reflect.Uint64:  fuzzUint,
	reflect.Uintptr: fuzzUint,
	reflect.Float32: func(v reflect.Value, r *rand.Rand) {
		v.SetFloat(float64(r.Float32()))
	},
	reflect.Float64: func(v reflect.Value, r *rand.Rand) {
		v.SetFloat(r.Float64())
	},
	reflect.Complex64: func(v reflect.Value, r *rand.Rand) {
		panic("unimplemented")
	},
	reflect.Complex128: func(v reflect.Value, r *rand.Rand) {
		panic("unimplemented")
	},
	reflect.String: func(v reflect.Value, r *rand.Rand) {
		v.SetString(RandString(r))
	},
	reflect.UnsafePointer: func(v reflect.Value, r *rand.Rand) {
		panic("unimplemented")
	},
}

// RandBool returns true or false randomly.
func RandBool(r *rand.Rand) bool {
	if r.Int()&1 == 1 {
		return true
	}
	return false
}

type charRange struct {
	first, last rune
}

// choose returns a random unicode character from the given range, using the
// given randomness source.
func (r *charRange) choose(rand *rand.Rand) rune {
	count := int64(r.last - r.first)
	return r.first + rune(rand.Int63n(count))
}

var unicodeRanges = []charRange{
	{' ', '~'},           // ASCII characters
	{'\u00a0', '\u02af'}, // Multi-byte encoded characters
	{'\u4e00', '\u9fff'}, // Common CJK (even longer encodings)
}

// RandString makes a random string up to 20 characters long. The returned string
// may include a variety of (valid) UTF-8 encodings. For testing.
func RandString(r *rand.Rand) string {
	n := r.Intn(20)
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = unicodeRanges[r.Intn(len(unicodeRanges))].choose(r)
	}
	return string(runes)
}

// RandUint64 makes random 64 bit numbers.
// Weirdly, rand doesn't have a function that gives you 64 random bits.
func RandUint64(r *rand.Rand) uint64 {
	return uint64(r.Uint32())<<32 | uint64(r.Uint32())
}
