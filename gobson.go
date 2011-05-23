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
	"encoding/binary"
	"encoding/hex"
	"crypto/md5"
	"runtime"
	"reflect"
	"strings"
	"sync/atomic"
	"sync"
	"time"
	"fmt"
	"os"
)

// --------------------------------------------------------------------------
// The public API.

// Objects implementing the bson.Getter interface will get the GetBSON()
// method called when the given value has to be marshalled, and the result
// of this method will be marshaled in place of the actual object.
type Getter interface {
	GetBSON() interface{}
}

// Objects implementing the bson.Setter interface will receive the BSON
// value via the SetBSON method during unmarshaling, and will not be
// changed as usual.  If setting the value works, the method should
// return true.  If it returns false, the given value will be omitted
// from maps and slices.
type Setter interface {
	SetBSON(v interface{}) (ok bool)
}

// Handy alias for a map[string]interface{} map, useful for dealing with BSON
// in a native way.  For instance:
//
//     bson.M{"a": 1, "b": true}
//
// There's no special handling for this type in addition to what's done anyway
// for an equivalent map type.  Elements in the map will be dumped in an
// undefined ordered. See also the bson.D type for an ordered alternative.
type M map[string]interface{}

// Type for dealing with documents containing ordered elements in a native
// fashion. For instance:
//
//     bson.D{{"a", 1}, {"b", true}}
//
// In some situations, such as when creating indexes for MongoDB, the order in
// which the elements are defined is important.  If the order is not important,
// using a map is generally more comfortable (see the bson.M type and the
// Map() method for D).
type D []DocElem

// See the bson.D type.
type DocElem struct {
	Name  string
	Value interface{}
}

// Raw may be used to work with raw unprocessed BSON documents and elements,
// if necessary in advanced cases.  Kind is the kind of element as defined
// per the BSON specification, and Data is the raw unprocessed data for
// the respective element.
//
// Relevant documentation:
//
//     http://bsonspec.org/#/specification
//
type Raw struct {
	Kind byte
	Data []byte
}

// Build a map[string]interface{} out of the ordered element name/value pairs.
func (d D) Map() (m M) {
	m = make(M, len(d))
	for _, item := range d {
		m[item.Name] = item.Value
	}
	return m
}

// Unique ID identifying the BSON object. Must be exactly 12 bytes long.
// MongoDB objects by default have such a property set in their "_id"
// property.
//
// http://www.mongodb.org/display/DOCS/Object+IDs
type ObjectId string

// ObjectIdHex returns an ObjectId from the provided hex representation.
// Calling this function with an invalid hex representation will
// cause a runtime panic.
func ObjectIdHex(s string) ObjectId {
	d, err := hex.DecodeString(s)
	if err != nil || len(d) != 12 {
		panic(fmt.Sprintf("Invalid input to ObjectIdHex: %q", s))
	}
	return ObjectId(d)
}

// objectIdCounter is atomically incremented when generating a new ObjectId
// using NewObjectId() function. It's used as a counter part of an id.
var objectIdCounter uint32 = 0

// machineId stores machine id generated once and used in subsequent calls
// to NewObjectId function.
var machineId []byte

// initMachineId generates machine id and puts it into the machineId global
// variable. If this function fails to get the hostname, it will cause
// a runtime error.
func initMachineId() {
	var sum [3]byte
	hostname, err := os.Hostname()
	if err != nil {
		panic("Failed to get hostname: " + err.String())
	}
	hw := md5.New()
	hw.Write([]byte(hostname))
	copy(sum[:3], hw.Sum())
	machineId = sum[:]
}

// NewObjectId generates and returns a new unique ObjectId.
// This function causes a runtime error if it fails to get the hostname
// of the current machine.
func NewObjectId() ObjectId {
	b := make([]byte, 12)
	// Timestamp, 4 bytes, big endian
	binary.BigEndian.PutUint32(b, uint32(time.Seconds()))
	// Machine, first 3 bytes of md5(hostname)
	if machineId == nil {
		initMachineId()
	}
	b[4] = machineId[0]
	b[5] = machineId[1]
	b[6] = machineId[2]
	// Pid, 2 bytes, specs don't specify endianness, but we use big endian.
	pid := os.Getpid()
	b[7] = byte(pid >> 8)
	b[8] = byte(pid)
	// Increment, 3 bytes, big endian
	i := atomic.AddUint32(&objectIdCounter, 1)
	b[9] = byte(i >> 16)
	b[10] = byte(i >> 8)
	b[11] = byte(i)
	return ObjectId(b)
}

// NewObjectIdSeconds returns a dummy ObjectId with the timestamp part filled
// with the provided number of seconds from epoch UTC, and all other parts
// filled with zeroes. It's not safe to insert a document with an id generated
// by this method, it is useful only for queries to find documents with ids
// generated before or after the specified timestamp.
func NewObjectIdSeconds(sec int32) ObjectId {
	var b [12]byte
	binary.BigEndian.PutUint32(b[:4], uint32(sec))
	return ObjectId(string(b[:]))
}

// String returns a hex string representation of the id.
// Example: ObjectIdHex("4d88e15b60f486e428412dc9").
func (id ObjectId) String() string {
	return `ObjectIdHex("` + hex.EncodeToString([]byte(string(id))) + `")`
}

// Valid returns true if the id is valid (contains exactly 12 bytes)
func (id ObjectId) Valid() bool {
	return len(id) == 12
}

// byteSlice returns byte slice of id from start to end.
// Calling this function with an invalid id will cause a runtime panic.
func (id ObjectId) byteSlice(start, end int) []byte {
	if len(id) != 12 {
		panic(fmt.Sprintf("Invalid ObjectId: %q", string(id)))
	}
	return []byte(string(id)[start:end])
}

// Timestamp returns the timestamp part of the id (the number of seconds
// from epoch in UTC). 
// It's a runtime error to call this method with an invalid id.
func (id ObjectId) Timestamp() int32 {
	// First 4 bytes of ObjectId is 32-bit big-endian timestamp
	return int32(binary.BigEndian.Uint32(id.byteSlice(0, 4)))
}

// Machine returns the 3-byte machine id part of the id.
// It's a runtime error to call this method with an invalid id.
func (id ObjectId) Machine() []byte {
	return id.byteSlice(4, 7)
}

// Pid returns the process id part of the id.
// It's a runtime error to call this method with an invalid id.
func (id ObjectId) Pid() uint16 {
	return binary.BigEndian.Uint16(id.byteSlice(7, 9))
}

// Counter returns the incrementing value part of the id.
// It's a runtime error to call this method with an invalid id.
func (id ObjectId) Counter() int32 {
	b := id.byteSlice(9, 12)
	// Counter is stored as big-endian 3-byte value
	return int32(uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]))
}

func (id ObjectId) ToString() string {
	return hex.EncodeToString([]byte(string(id)))
}

// Similar to a string, but used in languages with a distinct symbol type. This
// is an alias to a string type, so it can be used in string contexts and
// string(symbol) will work correctly.
type Symbol string

// UTC timestamp defined as nanoseconds since the traditional epoch time.  The
// internal MongoDB representation stores this value as milliseconds, so some
// precision will be lost when sending a Go value to MongoDB, but given that
// Go most commonly uses nanoseconds in time-related operations, this conversion
// is convenient.
type Timestamp int64

// Now returns a Timestamp value with the current time in nanoseconds.
func Now() Timestamp {
	// The value is stored in MongoDB as milliseconds, so truncate the value
	// ahead of time to avoid surprises after a roundtrip.
	return Timestamp(time.Nanoseconds() / 1e6 * 1e6)
}

// Special internal type used by MongoDB which for some strange reason has its
// own datatype defined in BSON.
type MongoTimestamp int64

type orderKey int64

// Special value which compares higher than all other possible BSON values.
var MaxKey = orderKey(1<<63 - 1)

// Special value which compares lower than all other possible BSON values.
var MinKey = orderKey(-1 << 63)

type undefined struct{}

var Undefined undefined

// Representation for non-standard binary values.  Any kind should work,
// but the following are known as of this writing:
//
//   0x00 - Generic. This is decoded as []byte(data), not Binary{0x00, data}.
//   0x01 - Function (!?)
//   0x02 - Obsolete generic.
//   0x03 - UUID
//   0x05 - MD5
//   0x80 - User defined.
//
type Binary struct {
	Kind byte
	Data []byte
}

// A special type for regular expressions.  The Options field should contain
// individual characters defining the way in which the pattern should be
// applied, and must be sorted. Valid options as of this writing are 'i' for
// case insensitive matching, 'm' for multi-line matching, 'x' for verbose
// mode, 'l' to make \w, \W, and similar be locale-dependent, 's' for dot-all
// mode (a '.' matches everything), and 'u' to make \w, \W, and similar match
// unicode. The value of the Options parameter is not verified before being
// marshaled into the BSON format.
type RegEx struct {
	Pattern string
	Options string
}

// Special type for JavaScript code.  If Scope is non-nil, it will be marshaled
// as a mapping from identifiers to values which should be used when evaluating
// the provided Code.
type JS struct {
	Code  string
	Scope interface{}
}


const initialBufferSize = 64

func handleErr(err *os.Error) {
	if r := recover(); r != nil {
		if _, ok := r.(runtime.Error); ok {
			panic(r)
		} else if s, ok := r.(string); ok {
			*err = os.ErrorString(s)
		} else if e, ok := r.(os.Error); ok {
			*err = e
		} else {
			panic(r)
		}
	}
}


// Marshal serializes the in document, which may be a map or a struct value.
// In the case of struct values, only exported fields will be serialized.
// These fields may optionally have tags to define the serialization key for
// the respective fields.  Without a tag, the lowercased field name is used
// as the key for each field.  If a field tag ends in "/c", that field will
// only be serialized if it's not set to the zero value for the field type.
// If a field tag ends with the "/s" suffix, an int64 value in the given
// field will be serialized as an int32 if possible.
func Marshal(in interface{}) (out []byte, err os.Error) {
	defer handleErr(&err)
	e := &encoder{make([]byte, 0, initialBufferSize)}
	e.addDoc(reflect.ValueOf(in))
	return e.out, nil
}

// Unmarshal deserializes data from in into the out value.  The out value
// must be a map or a pointer to a struct (or a pointer to a struct pointer).
// In the case of struct values, field names are mapped to the struct using
// the field tag as the key.  If the field has no tag, its lowercased name
// will be used as the default key.  Nil values are properly initialized
// when necessary.
//
// The target field types of out may not necessarily match the BSON values
// of the provided data.  If there is a sensible way to unmarshal the values
// into the Go types, they will be converted.  Otherwise, the incompatible
// values will be silently skipped.
func Unmarshal(in []byte, out interface{}) (err os.Error) {
	defer handleErr(&err)
	v := reflect.ValueOf(out)
	switch v.Kind() {
	case reflect.Map, reflect.Ptr:
		d := &decoder{in: in}
		d.readDocTo(v)
	case reflect.Struct:
		return os.ErrorString("Unmarshal can't deal with struct values. Use a pointer.")
	default:
		return os.ErrorString("Unmarshal needs a map or a pointer to a struct.")
	}
	return nil
}

// Unmarshal deserializes raw into the out value.  In addition to whole
// documents, Raw's Unmarshal may also be used to unmarshal the data for
// individual elements within a partially unmarshalled document.  This
// enables parts of a document to be lazily and conditionally deserialized.
// 
// If the out value type is not compatible with raw, a *bson.TypeError
// is returned.
func (raw Raw) Unmarshal(out interface{}) (err os.Error) {
	defer handleErr(&err)
	v := reflect.ValueOf(out)
	switch v.Kind() {
	case reflect.Map, reflect.Ptr:
		d := &decoder{in: raw.Data}
		good := d.readElemTo(v, raw.Kind)
		if !good {
			return &TypeError{v.Type(), raw.Kind}
		}
	default:
		return os.ErrorString("Raw Unmarshal needs a map or a valid pointer.")
	}
	return nil
}

type TypeError struct {
	Type reflect.Type
	Kind byte
}

func (e *TypeError) String() string {
	return fmt.Sprintf("BSON kind 0x%02x isn't compatible with type %s", e.Kind, e.Type.String())
}

// --------------------------------------------------------------------------
// Maintain a mapping of keys to structure field indexes

type structFields struct {
	Map  map[string]fieldInfo
	List []fieldInfo
}

type fieldInfo struct {
	Key         string
	Num         int
	Conditional bool
	Short       bool
}

var fieldMap = make(map[string]*structFields)
var fieldMapMutex sync.RWMutex

func getStructFields(st reflect.Type) (*structFields, os.Error) {
	path := st.PkgPath()
	name := st.Name()

	fullName := path + "." + name
	fieldMapMutex.RLock()
	fields, found := fieldMap[fullName]
	fieldMapMutex.RUnlock()
	if found {
		return fields, nil
	}

	n := st.NumField()
	fieldsMap := make(map[string]fieldInfo)
	fieldsList := make([]fieldInfo, n)
	for i := 0; i != n; i++ {
		field := st.Field(i)
		if field.PkgPath != "" {
			continue // Private field
		}

		info := fieldInfo{Num: i}

		if s := strings.LastIndex(field.Tag, "/"); s != -1 {
			for _, c := range field.Tag[s+1:] {
				switch c {
				case int('c'):
					info.Conditional = true
				case int('s'):
					info.Short = true
				default:
					panic("Unsupported field flag: " + string([]int{c}))
				}
			}
			field.Tag = field.Tag[:s]
		}

		if field.Tag != "" {
			info.Key = field.Tag
		} else {
			info.Key = strings.ToLower(field.Name)
		}

		if _, found = fieldsMap[info.Key]; found {
			msg := "Duplicated key '" + info.Key + "' in struct " + st.String()
			return nil, os.NewError(msg)
		}

		fieldsList[len(fieldsMap)] = info
		fieldsMap[info.Key] = info
	}

	fields = &structFields{fieldsMap, fieldsList[:len(fieldsMap)]}

	if fullName != "." {
		fieldMapMutex.Lock()
		fieldMap[fullName] = fields
		fieldMapMutex.Unlock()
	}

	return fields, nil
}
