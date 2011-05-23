// gobson - BSON library for Go.
// 
// Copyright (c) 2010-2011 - Gustavo Niemeyer <gustavo@niemeyer.net>
// 
// All rights reserved.
// 
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
// 
//     * Redistributions of source code must retain the above copyright notice,
//       this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above copyright notice,
//       this list of conditions and the following disclaimer in the documentation
//       and/or other materials provided with the distribution.
//     * Neither the name of the copyright holder nor the names of its
//       contributors may be used to endorse or promote products derived from
//       this software without specific prior written permission.
// 
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
// CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package bson

import (
	"strconv"
	"reflect"
	"math"
)

// --------------------------------------------------------------------------
// Some internal infrastructure.

var (
	typeRegEx          reflect.Type
	typeBinary         reflect.Type
	typeObjectId       reflect.Type
	typeSymbol         reflect.Type
	typeTimestamp      reflect.Type
	typeMongoTimestamp reflect.Type
	typeOrderKey       reflect.Type
	typeDocElem        reflect.Type
	typeRaw            reflect.Type
)

const itoaCacheSize = 32

var itoaCache []string

func init() {
	typeBinary = reflect.TypeOf(Binary{})
	typeObjectId = reflect.TypeOf(ObjectId(""))
	typeSymbol = reflect.TypeOf(Symbol(""))
	typeTimestamp = reflect.TypeOf(Timestamp(0))
	typeMongoTimestamp = reflect.TypeOf(MongoTimestamp(0))
	typeOrderKey = reflect.TypeOf(MinKey)
	typeDocElem = reflect.TypeOf(DocElem{})
	typeRaw = reflect.TypeOf(Raw{})

	itoaCache = make([]string, itoaCacheSize)
	for i := 0; i != itoaCacheSize; i++ {
		itoaCache[i] = strconv.Itoa(i)
	}
}

func itoa(i int) string {
	if i < itoaCacheSize {
		return itoaCache[i]
	}
	return strconv.Itoa(i)
}


// --------------------------------------------------------------------------
// Marshaling of the document value itself.

type encoder struct {
	out []byte
}

func (e *encoder) addDoc(v reflect.Value) {
	for {
		if vi, ok := v.Interface().(Getter); ok {
			v = reflect.ValueOf(vi.GetBSON())
			continue
		}
		if v.Kind() == reflect.Ptr {
			v = v.Elem()
			continue
		}
		break
	}

	if v.Type() == typeRaw {
		raw := v.Interface().(Raw)
		if raw.Kind != 0x03 && raw.Kind != 0x00 {
			panic("Attempted to unmarshal Raw kind " + strconv.Itoa(int(raw.Kind)) + " as a document")
		}
		e.addBytes(raw.Data...)
		return
	}

	start := e.reserveInt32()

	switch v.Kind() {
	case reflect.Map:
		e.addMap(v)
	case reflect.Struct:
		e.addStruct(v)
	case reflect.Array, reflect.Slice:
		e.addSlice(v)
	default:
		panic("Can't marshal " + v.Type().String() + " as a BSON document")
	}

	e.addBytes(0)
	e.setInt32(start, int32(len(e.out)-start))
}

func (e *encoder) addMap(v reflect.Value) {
	for _, k := range v.MapKeys() {
		e.addElem(k.String(), v.MapIndex(k), false)
	}
}

func (e *encoder) addStruct(v reflect.Value) {
	fields, err := getStructFields(v.Type())
	if err != nil {
		panic(err)
	}
	for i, info := range fields.List {
		value := v.Field(i)
		if info.Conditional && isZero(value) {
			continue
		}
		e.addElem(info.Key, value, info.Short)
	}
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return len(v.String()) == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice:
		return v.Len() == 0
	case reflect.Map:
		return v.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Bool:
		return !v.Bool()
	}
	return false
}

func (e *encoder) addSlice(v reflect.Value) {
	if d, ok := v.Interface().(D); ok {
		for _, elem := range d {
			e.addElem(elem.Name, reflect.ValueOf(elem.Value), false)
		}
	} else {
		for i := 0; i != v.Len(); i++ {
			e.addElem(itoa(i), v.Index(i), false)
		}
	}
}


// --------------------------------------------------------------------------
// Marshaling of elements in a document.

func (e *encoder) addElemName(kind byte, name string) {
	e.addBytes(kind)
	e.addBytes([]byte(name)...)
	e.addBytes(0)
}

func (e *encoder) addElem(name string, v reflect.Value, short bool) {

	if !v.IsValid() {
		e.addElemName('\x0A', name)
		return
	}

	if getter, ok := v.Interface().(Getter); ok {
		e.addElem(name, reflect.ValueOf(getter.GetBSON()), short)
		return
	}

	switch v.Kind() {

	case reflect.Interface:
		e.addElem(name, v.Elem(), short)

	case reflect.Ptr:
		e.addElem(name, v.Elem(), short)

	case reflect.String:
		s := v.String()

		switch v.Type() {

		case typeObjectId:
			if len(s) != 12 {
				panic("ObjectIDs must be exactly 12 bytes long (got " +
					strconv.Itoa(len(s)) + ")")
			}
			e.addElemName('\x07', name)
			e.addBytes([]byte(s)...)

		case typeSymbol:
			e.addElemName('\x0E', name)
			e.addStr(s)

		default:
			e.addElemName('\x02', name)
			e.addStr(s)
		}

	case reflect.Float32, reflect.Float64:
		e.addElemName('\x01', name)
		e.addInt64(int64(math.Float64bits(v.Float())))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := v.Uint()
		if int64(u) < 0 {
			panic("BSON has no uint64 type, and value is too large to fit correctly in an int64")
		} else if u <= math.MaxInt32 && (short || v.Kind() <= reflect.Uint32) {
			e.addElemName('\x10', name)
			e.addInt32(int32(u))
		} else {
			e.addElemName('\x12', name)
			e.addInt64(int64(u))
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Type().Kind() <= reflect.Int32 {
			e.addElemName('\x10', name)
			e.addInt32(int32(v.Int()))
		} else {
			switch v.Type() {

			case typeTimestamp:
				// MongoDB wants timestamps as milliseconds.
				// Go likes nanoseconds.  Convert them.
				e.addElemName('\x09', name)
				e.addInt64(v.Int() / 1e6)

			case typeMongoTimestamp:
				e.addElemName('\x11', name)
				e.addInt64(v.Int())

			case typeOrderKey:
				if v.Int() == int64(MaxKey) {
					e.addElemName('\x7F', name)
				} else {
					e.addElemName('\xFF', name)
				}

			default:
				i := v.Int()
				if short && i >= math.MinInt32 && i <= math.MaxInt32 {
					// It fits into an int32, encode as such.
					e.addElemName('\x10', name)
					e.addInt32(int32(i))
				} else {
					e.addElemName('\x12', name)
					e.addInt64(i)
				}
			}
		}

	case reflect.Bool:
		e.addElemName('\x08', name)
		if v.Bool() {
			e.addBytes(1)
		} else {
			e.addBytes(0)
		}

	case reflect.Map:
		e.addElemName('\x03', name)
		e.addDoc(v)

	case reflect.Slice:
		vt := v.Type()
		et := vt.Elem()
		if et.Kind() == reflect.Uint8 {
			// FIXME: This breaks down with custom types based on []byte
			e.addElemName('\x05', name)
			e.addBinary('\x00', v.Interface().([]byte))
		} else if et == typeDocElem {
			e.addElemName('\x03', name)
			e.addDoc(v)
		} else {
			e.addElemName('\x04', name)
			e.addDoc(v)
		}

	case reflect.Array:
		et := v.Type().Elem()
		if et.Kind() == reflect.Uint8 {
			e.addElemName('\x05', name)
			e.addBinary('\x00', v.Slice(0, v.Len()).Interface().([]byte))
		} else {
			e.addElemName('\x04', name)
			e.addDoc(v)
		}

	case reflect.Struct:
		switch s := v.Interface().(type) {

		case Raw:
			kind := s.Kind
			if kind == 0x00 {
				kind = 0x03
			}
			e.addElemName(kind, name)
			e.addBytes(s.Data...)

		case Binary:
			e.addElemName('\x05', name)
			e.addBinary(s.Kind, s.Data)

		case RegEx:
			e.addElemName('\x0B', name)
			e.addCStr(s.Pattern)
			e.addCStr(s.Options)

		case JS:
			if s.Scope == nil {
				e.addElemName('\x0D', name)
				e.addStr(s.Code)
			} else {
				e.addElemName('\x0F', name)
				start := e.reserveInt32()
				e.addStr(s.Code)
				e.addDoc(reflect.ValueOf(s.Scope))
				e.setInt32(start, int32(len(e.out)-start))
			}

		case undefined:
			e.addElemName('\x06', name)

		default:
			e.addElemName('\x03', name)
			e.addDoc(v)
		}

	default:
		panic("Can't marshal " + v.Type().String() + " in a BSON document")
	}
}


// --------------------------------------------------------------------------
// Marshaling of base types.

func (e *encoder) addBinary(subtype byte, v []byte) {
	if subtype == 0x02 {
		// Wonder how that brilliant idea came to life. Obsolete, luckily.
		e.addInt32(int32(len(v) + 4))
		e.addBytes(subtype)
		e.addInt32(int32(len(v)))
	} else {
		e.addInt32(int32(len(v)))
		e.addBytes(subtype)
	}
	e.addBytes(v...)
}

func (e *encoder) addStr(v string) {
	e.addInt32(int32(len(v) + 1))
	e.addCStr(v)
}

func (e *encoder) addCStr(v string) {
	e.addBytes([]byte(v)...)
	e.addBytes(0)
}

func (e *encoder) reserveInt32() (pos int) {
	pos = len(e.out)
	e.addBytes(0, 0, 0, 0)
	return pos
}

func (e *encoder) setInt32(pos int, v int32) {
	e.out[pos+0] = byte(v)
	e.out[pos+1] = byte(v >> 8)
	e.out[pos+2] = byte(v >> 16)
	e.out[pos+3] = byte(v >> 24)
}

func (e *encoder) addInt32(v int32) {
	u := uint32(v)
	e.addBytes(byte(u), byte(u>>8), byte(u>>16), byte(u>>24))
}

func (e *encoder) addInt64(v int64) {
	u := uint64(v)
	e.addBytes(byte(u), byte(u>>8), byte(u>>16), byte(u>>24),
		byte(u>>32), byte(u>>40), byte(u>>48), byte(u>>56))
}

func (e *encoder) addBytes(v ...byte) {
	e.out = append(e.out, v...)
}
