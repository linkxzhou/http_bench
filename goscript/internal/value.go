package internal

import (
	_ "fmt"
	"reflect"
	"strings"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type Value interface {
	Elem() Value
	Interface() interface{}
	String() string
	Int() int64
	Uint() uint64
	Float() float64
	Index(i int) Value
	MapIndex(v Value) Value
	Set(Value)
	Len() int
	Cap() int
	Type() reflect.Type
	IsValid() bool
	IsNil() bool
	Bool() bool
	Field(i int) Value
	Next() Value
	Kind() reflect.Kind

	RValue() reflect.Value
}

type ExternalValue struct {
	ssa.Value
	Object *ExternalObject
}

func (p *ExternalValue) Store(v Value) {
	p.Object.Value.Elem().Set(v.RValue())
}

func (p *ExternalValue) ToValue() Value {
	return RValue{p.Object.Value}
}

func (p *ExternalValue) Interface() interface{} {
	return p.Object
}

type RValue struct {
	reflect.Value
}

func (p RValue) RValue() reflect.Value {
	return p.Value
}

func (p RValue) Next() Value {
	panic("implement")
}

func (p RValue) Field(i int) Value {
	return RValue{p.Value.Field(i)}
}

func (p RValue) MapIndex(v Value) Value {
	return RValue{p.Value.MapIndex(v.RValue())}
}

func (p RValue) Set(v Value) {
	p.Value.Set(v.RValue())
}

func (p RValue) Index(i int) Value {
	return RValue{p.Value.Index(i)}
}

func (p RValue) Elem() Value {
	if p.Value.Kind() == reflect.Ptr || p.Value.Kind() == reflect.Interface {
		return RValue{p.Value.Elem()}
	}
	return p
}

func (p RValue) IsNil() bool {
	switch p.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Ptr, reflect.UnsafePointer, reflect.Interface, reflect.Slice:
		return p.Value.IsNil()
	default:
		return false
	}
}

type MapIter struct {
	I int
	Value
	Keys []reflect.Value
}

func (p *MapIter) Next() Value {
	v := make([]Value, 3)
	if p.I < len(p.Keys) {
		k := RValue{p.Keys[p.I]}
		v[0] = ValueOf(true)
		v[1] = k
		v[2] = p.MapIndex(k)
		p.I++
	} else {
		v[0] = ValueOf(false)
	}
	return ValueOf(v)
}

func ValueOf(v interface{}) Value {
	return RValue{reflect.ValueOf(v)}
}

func Package(values []reflect.Value) Value {
	l := len(values)
	switch l {
	case 0:
		return nil
	case 1:
		return RValue{values[0]}
	default:
		v := make([]Value, l)
		for i := range v {
			v[i] = RValue{values[i]}
		}
		return ValueOf(v)
	}
}

func Unpackage(val Value) []reflect.Value {
	if val == nil {
		return nil
	}
	if arr, ok := val.Interface().([]Value); ok {
		ret := make([]reflect.Value, len(arr))
		for i, v := range arr {
			ret[i] = v.RValue()
		}
		return ret
	}
	return []reflect.Value{val.RValue()}
}

func ExternalValueWrap(p *Importer, pkg *ssa.Package) {
	for f := range ssautil.AllFunctions(pkg.Prog) {
		for _, block := range f.Blocks {
			for _, instr := range block.Instrs {
				for _, v := range instr.Operands(nil) {
					valueWrap(p, v)
				}
			}
		}
	}
}

func valueWrap(p *Importer, v *ssa.Value) {
	if v == nil {
		return
	}
	name := strings.TrimLeft((*v).String(), "*&")
	dotIndex := strings.IndexRune(name, '.')
	if dotIndex < 0 {
		return
	}
	pkgName := name[:dotIndex]
	if pkg := p.SsaPackage(pkgName); pkg != nil {
		if value, ok := pkg.Members[name[dotIndex+1:]].(ssa.Value); ok {
			*v = value
			return
		}
	}
	if external := p.ExternalObject(name); external != nil {
		*v = &ExternalValue{
			Value:  *v,
			Object: external,
		}
		return
	}
}
