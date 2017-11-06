package kingpin

//go:generate go run ./cmd/genvalues/main.go

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/alecthomas/units"
)

// NOTE: Most of the base type values were lifted from:
// http://golang.org/src/pkg/flag/flag.go?s=20146:20222

// Value is the interface to the dynamic value stored in a flag.
// (The default value is represented as a string.)
//
// If a Value has an IsBoolFlag() bool method returning true, the command-line
// parser makes --name equivalent to -name=true rather than using the next
// command-line argument, and adds a --no-name counterpart for negating the
// flag.
type Value interface {
	String() string
	Set(string) error
}

// Getter is an interface that allows the contents of a Value to be retrieved.
// It wraps the Value interface, rather than being part of it, because it
// appeared after Go 1 and its compatibility rules. All Value types provided
// by this package satisfy the Getter interface.
type Getter interface {
	Value
	Get() interface{}
}

// Optional interface to indicate boolean flags that don't accept a value, and
// implicitly have a --no-<x> negation counterpart.
//
// This is for compatibility with the stdlib.
type boolFlag interface {
	Value
	IsBoolFlag() bool
}

// BoolFlag is an optional interface to specify that a flag is a boolean flag.
type BoolFlag interface {
	// Specify if the flag is negatable (ie. supports both --no-<name> and --name).
	BoolFlagIsNegatable() bool
}

// Optional interface for values that cumulatively consume all remaining
// input.
type cumulativeValue interface {
	Value
	Reset()
	IsCumulative() bool
}

type accumulatorOptions struct {
	separator string
}

func newAccumulatorOptions(options ...AccumulatorOption) (out accumulatorOptions) {
	for _, option := range options {
		option(&out)
	}
	return
}

type AccumulatorOption func(a *accumulatorOptions)

// Separator configures an accumulating value to split on this value.
func Separator(separator string) AccumulatorOption {
	return func(a *accumulatorOptions) {
		a.separator = separator
	}
}

type accumulator struct {
	element func(value interface{}) Value
	typ     reflect.Type
	slice   reflect.Value
	accumulatorOptions
}

func isBoolFlag(f Value) bool {
	if bf, ok := f.(boolFlag); ok {
		return bf.IsBoolFlag()
	}
	_, ok := f.(BoolFlag)
	return ok
}

// Use reflection to accumulate values into a slice.
//
// target := []string{}
// newAccumulator(&target, func (value interface{}) Value {
//   return newStringValue(value.(*string))
// })
func newAccumulator(slice interface{}, options []AccumulatorOption, element func(value interface{}) Value) *accumulator {
	typ := reflect.TypeOf(slice)
	if typ.Kind() != reflect.Ptr || typ.Elem().Kind() != reflect.Slice {
		panic(T("expected a pointer to a slice"))
	}
	return &accumulator{
		element:            element,
		typ:                typ.Elem().Elem(),
		slice:              reflect.ValueOf(slice),
		accumulatorOptions: newAccumulatorOptions(options...),
	}
}

func (a *accumulator) String() string {
	out := []string{}
	s := a.slice.Elem()
	for i := 0; i < s.Len(); i++ {
		out = append(out, a.element(s.Index(i).Addr().Interface()).String())
	}
	return strings.Join(out, ",")
}

func (a *accumulator) Set(value string) error {
	values := []string{}
	if a.separator == "" {
		values = append(values, value)
	} else {
		values = append(values, strings.Split(value, a.separator)...)
	}
	for _, v := range values {
		e := reflect.New(a.typ)
		if err := a.element(e.Interface()).Set(v); err != nil {
			return err
		}
		slice := reflect.Append(a.slice.Elem(), e.Elem())
		a.slice.Elem().Set(slice)
	}
	return nil
}

func (a *accumulator) Get() interface{} {
	return a.slice.Interface()
}

func (a *accumulator) IsCumulative() bool {
	return true
}

func (a *accumulator) Reset() {
	if a.slice.Kind() == reflect.Ptr {
		a.slice.Elem().Set(reflect.MakeSlice(a.slice.Type().Elem(), 0, 0))
	} else {
		a.slice.Set(reflect.MakeSlice(a.slice.Type(), 0, 0))
	}
}

func (b *boolValue) BoolFlagIsNegatable() bool { return false }

// -- A boolean flag that can not be negated.
func (n *negatableBoolValue) BoolFlagIsNegatable() bool { return true }

// -- map[string]string Value
type stringMapValue struct {
	values *map[string]string
	accumulatorOptions
}

func newStringMapValue(p *map[string]string, options ...AccumulatorOption) *stringMapValue {
	return &stringMapValue{
		values:             p,
		accumulatorOptions: newAccumulatorOptions(options...),
	}
}

var stringMapRegex = regexp.MustCompile("[:=]")

func (s *stringMapValue) Set(value string) error {
	values := []string{}
	if s.separator == "" {
		values = append(values, value)
	} else {
		values = append(values, strings.Split(value, s.separator)...)
	}
	for _, v := range values {
		parts := stringMapRegex.Split(v, 2)
		if len(parts) != 2 {
			return TError("expected KEY=VALUE got '{{.Arg0}}'", V{"Arg0": v})
		}
		(*s.values)[parts[0]] = parts[1]
	}
	return nil
}

func (s *stringMapValue) Get() interface{} {
	return *s.values
}

func (s *stringMapValue) String() string {
	return fmt.Sprintf("%s", *s.values)
}

func (s *stringMapValue) IsCumulative() bool {
	return true
}

func (s *stringMapValue) Reset() {
	*s.values = map[string]string{}
}

// -- existingFile Value

type fileStatValue struct {
	path      *string
	predicate func(os.FileInfo) error
}

func newFileStatValue(p *string, predicate func(os.FileInfo) error) *fileStatValue {
	return &fileStatValue{
		path:      p,
		predicate: predicate,
	}
}

func (f *fileStatValue) Set(value string) error {
	if s, err := os.Stat(value); os.IsNotExist(err) {
		return TError("path '{{.Arg0}}' does not exist", V{"Arg0": value})
	} else if err != nil {
		return err
	} else if err := f.predicate(s); err != nil {
		return err
	}
	*f.path = value
	return nil
}

func (f *fileStatValue) Get() interface{} {
	return (string)(*f.path)
}

func (f *fileStatValue) String() string {
	return *f.path
}

// -- net.IP Value
type ipValue net.IP

func newIPValue(p *net.IP) *ipValue {
	return (*ipValue)(p)
}

func (i *ipValue) Set(value string) error {
	ip := net.ParseIP(value)
	if ip == nil {
		return fmt.Errorf("'%s' is not an IP address", value)
	}
	*i = *(*ipValue)(&ip)
	return nil
}

func (i *ipValue) Get() interface{} {
	return (net.IP)(*i)
}

func (i *ipValue) String() string {
	return (*net.IP)(i).String()
}

// -- url.URL Value
type urlValue struct {
	u **url.URL
}

func newURLValue(p **url.URL) *urlValue {
	return &urlValue{p}
}

func (u *urlValue) Set(value string) error {
	url, err := url.Parse(value)
	if err != nil {
		return TError("invalid URL: {{.Arg0}}", V{"Arg0": err})
	}
	*u.u = url
	return nil
}

func (u *urlValue) Get() interface{} {
	return (*url.URL)(*u.u)
}

func (u *urlValue) String() string {
	if *u.u == nil {
		return T("<nil>")
	}
	return (*u.u).String()
}

// -- []*url.URL Value
type urlListValue []*url.URL

func newURLListValue(p *[]*url.URL) *urlListValue {
	return (*urlListValue)(p)
}

func (u *urlListValue) Set(value string) error {
	url, err := url.Parse(value)
	if err != nil {
		return TError("invalid URL: {{.Arg0}}", V{"Arg0": err})
	}
	*u = append(*u, url)
	return nil
}

func (u *urlListValue) Get() interface{} {
	return ([]*url.URL)(*u)
}

func (u *urlListValue) String() string {
	out := []string{}
	for _, url := range *u {
		out = append(out, url.String())
	}
	return strings.Join(out, ",")
}

// A flag whose value must be in a set of options.
type enumValue struct {
	value   *string
	options []string
}

func newEnumFlag(target *string, options ...string) *enumValue {
	return &enumValue{
		value:   target,
		options: options,
	}
}

func (e *enumValue) String() string {
	return *e.value
}

func (e *enumValue) Set(value string) error {
	for _, v := range e.options {
		if v == value {
			*e.value = value
			return nil
		}
	}
	return TError("enum value must be one of {{.Arg0}}, got '{{.Arg1}}'", V{"Arg0": strings.Join(e.options, T(",")), "Arg1": value})
}

func (e *enumValue) Get() interface{} {
	return (string)(*e.value)
}

// -- []string Enum Value
type enumsValue struct {
	value   *[]string
	options []string
}

func newEnumsFlag(target *[]string, options ...string) *enumsValue {
	return &enumsValue{
		value:   target,
		options: options,
	}
}

func (e *enumsValue) Set(value string) error {
	for _, v := range e.options {
		if v == value {
			*e.value = append(*e.value, value)
			return nil
		}
	}
	return TError("enum value must be one of {{.Arg0}}, got '{{.Arg1}}'", V{"Arg0": strings.Join(e.options, T(",")), "Arg1": value})
}

func (e *enumsValue) Get() interface{} {
	return ([]string)(*e.value)
}

func (e *enumsValue) String() string {
	return strings.Join(*e.value, ",")
}

func (e *enumsValue) IsCumulative() bool {
	return true
}

func (e *enumsValue) Reset() {
	*e.value = []string{}
}

// -- units.Base2Bytes Value
type bytesValue units.Base2Bytes

func newBytesValue(p *units.Base2Bytes) *bytesValue {
	return (*bytesValue)(p)
}

func (d *bytesValue) Set(s string) error {
	v, err := units.ParseBase2Bytes(s)
	*d = bytesValue(v)
	return err
}

func (d *bytesValue) Get() interface{} { return units.Base2Bytes(*d) }

func (d *bytesValue) String() string { return (*units.Base2Bytes)(d).String() }

func newExistingFileValue(target *string) *fileStatValue {
	return newFileStatValue(target, func(s os.FileInfo) error {
		if s.IsDir() {
			return TError("'{{.Arg0}}' is a directory", V{"Arg0": s.Name()})
		}
		return nil
	})
}

func newExistingDirValue(target *string) *fileStatValue {
	return newFileStatValue(target, func(s os.FileInfo) error {
		if !s.IsDir() {
			return TError("'{{.Arg0}}' is a file", V{"Arg0": s.Name()})
		}
		return nil
	})
}

func newExistingFileOrDirValue(target *string) *fileStatValue {
	return newFileStatValue(target, func(s os.FileInfo) error { return nil })
}

type counterValue int

func newCounterValue(n *int) *counterValue {
	return (*counterValue)(n)
}

func (c *counterValue) Set(s string) error {
	*c++
	return nil
}

func (c *counterValue) Get() interface{}   { return (int)(*c) }
func (c *counterValue) IsBoolFlag() bool   { return true }
func (c *counterValue) String() string     { return fmt.Sprintf("%d", *c) }
func (c *counterValue) IsCumulative() bool { return true }
func (c *counterValue) Reset()             { *c = 0 }
