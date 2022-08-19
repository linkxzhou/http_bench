package internal

import (
	"go/ast"

	"github.com/goccy/go-reflect"
)

type BasicKind int

const (
	Unknown BasicKind = iota
	Var
	Const
	TypeName
	Function
	BuiltinFunction
)

type ExternalPackage struct {
	Path    string
	Name    string
	Objects []*ExternalObject
}

type ExternalObject struct {
	Name  string
	Kind  BasicKind
	Value reflect.Value
	Type  reflect.Type
}

var packages = make(map[string]*ExternalPackage)
var packagesByName = make(map[string]*ast.ImportSpec)

func GetPackageByName(name string) *ast.ImportSpec {
	return packagesByName[name]
}

func GetAllPackages() map[string]*ExternalPackage {
	return packages
}
