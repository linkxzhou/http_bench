package internal

import (
	"github.com/goccy/go-reflect"
)

func AddPackage(path string, name string, objects ...*ExternalObject) {
	LoadPackage(path, name, objects...)
}

func NewFunction(name string, value interface{}, doc string) *ExternalObject {
	return &ExternalObject{
		Name:  name,
		Kind:  Function,
		Value: reflect.ValueOf(value),
		Type:  reflect.TypeOf(value),
	}
}

func NewVar(name string, valueAddr interface{}, typ reflect.Type, doc string) *ExternalObject {
	return &ExternalObject{
		Name:  name,
		Kind:  Var,
		Value: reflect.ValueOf(valueAddr),
		Type:  typ,
	}
}

func NewConst(name string, value interface{}, doc string) *ExternalObject {
	return &ExternalObject{
		Name:  name,
		Kind:  Const,
		Value: reflect.ValueOf(value),
		Type:  reflect.TypeOf(value),
	}
}

func NewType(name string, typ reflect.Type, doc string) *ExternalObject {
	return &ExternalObject{
		Name: name,
		Kind: TypeName,
		Type: typ,
	}
}
