package internal

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/modern-go/concurrent"
	"golang.org/x/tools/go/ssa"
)

type Any = interface{}
type Logger = func(fmt string, a ...Any)

var externalType *concurrent.Map = concurrent.NewMap()

func LoadPackage(path, name string, objects ...*ExternalObject) {
	packages[path] = &ExternalPackage{
		Path:    path,
		Name:    name,
		Objects: objects,
	}
	packagesByName[name] = &ast.ImportSpec{
		Path: &ast.BasicLit{
			Kind:  token.STRING,
			Value: fmt.Sprintf(`"%s"`, path),
		},
	}
}

func GetExternalType(t types.Type) reflect.Type {
	if v, ok := externalType.Load(t); ok {
		return v.(reflect.Type)
	}
	return nil
}

func SetExternalType(t types.Type, rType reflect.Type) {
	externalType.Store(t, rType)
}

type Importer struct {
	typeCache map[reflect.Type]types.Type
	types.Importer

	ssaPackages     map[string]*ssa.Package
	packageCache    map[string]*types.Package
	externalObjects map[string]*ExternalObject
}

func NewImporter(importPackage ...*ssa.Package) *Importer {
	i := &Importer{
		typeCache: map[reflect.Type]types.Type{
			reflect.TypeOf(func(bool) {}).In(0):       types.Typ[types.Bool],
			reflect.TypeOf(func(int) {}).In(0):        types.Typ[types.Int],
			reflect.TypeOf(func(int8) {}).In(0):       types.Typ[types.Int8],
			reflect.TypeOf(func(int16) {}).In(0):      types.Typ[types.Int16],
			reflect.TypeOf(func(int32) {}).In(0):      types.Typ[types.Int32],
			reflect.TypeOf(func(int64) {}).In(0):      types.Typ[types.Int64],
			reflect.TypeOf(func(uint) {}).In(0):       types.Typ[types.Uint],
			reflect.TypeOf(func(uint8) {}).In(0):      types.Typ[types.Uint8],
			reflect.TypeOf(func(uint16) {}).In(0):     types.Typ[types.Uint16],
			reflect.TypeOf(func(uint32) {}).In(0):     types.Typ[types.Uint32],
			reflect.TypeOf(func(uint64) {}).In(0):     types.Typ[types.Uint64],
			reflect.TypeOf(func(uintptr) {}).In(0):    types.Typ[types.Uintptr],
			reflect.TypeOf(func(float32) {}).In(0):    types.Typ[types.Float32],
			reflect.TypeOf(func(float64) {}).In(0):    types.Typ[types.Float64],
			reflect.TypeOf(func(complex64) {}).In(0):  types.Typ[types.Complex64],
			reflect.TypeOf(func(complex128) {}).In(0): types.Typ[types.Complex128],
			reflect.TypeOf(func(string) {}).In(0):     types.Typ[types.String],
		},
		externalObjects: make(map[string]*ExternalObject),
		ssaPackages:     make(map[string]*ssa.Package),
		packageCache:    make(map[string]*types.Package),
	}
	for _, pkg := range importPackage {
		i.ssaPackages[pkg.Pkg.Name()] = pkg
	}
	return i
}

func (p *Importer) Import(path string) (*types.Package, error) {
	if pkg := p.ssaPackages[path]; pkg != nil {
		return pkg.Pkg, nil
	}

	if pkg, ok := p.packageCache[path]; ok && pkg != nil {
		return pkg, nil
	}

	pkg := p.Package(path)
	importList := make([]*types.Package, 0)
	for _, importPkg := range p.packageCache {
		importList = append(importList, importPkg)
	}
	pkg.SetImports(importList)
	return pkg, nil
}

func (p *Importer) newObject(pkg *types.Package, nobj *ExternalObject) (object types.Object) {
	name := nobj.Name
	switch nobj.Kind {
	case TypeName:
		typ := p.typeOf(nobj.Type, pkg)
		object = types.NewTypeName(token.NoPos, pkg, name, typ)
	case Var:
		typ := p.typeOf(nobj.Type, pkg)
		object = types.NewVar(token.NoPos, pkg, name, typ)
		pkg.Scope().Insert(object)
	case Const:
		v := nobj.Value
		var constValue constant.Value
		switch nobj.Type.Kind() {
		case reflect.Bool:
			constValue = constant.MakeBool(v.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			constValue = constant.MakeInt64(v.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			constValue = constant.MakeUint64(v.Uint())
		case reflect.Float32, reflect.Float64:
			constValue = constant.MakeFloat64(v.Float())
		case reflect.String:
			constValue = constant.MakeString(v.String())
		case reflect.Complex64, reflect.Complex128:
			// TODO:
		}
		object = types.NewConst(token.NoPos, pkg, name, p.typeOf(nobj.Type, pkg), constValue)
		pkg.Scope().Insert(object)
	case Function, BuiltinFunction:
		typ := p.typeOf(nobj.Type, pkg)
		object = types.NewFunc(token.NoPos, pkg, name, typ.(*types.Signature))
		pkg.Scope().Insert(object)
	}
	if object == nil {
		fmt.Printf("%#v", nobj)
	}
	return object
}

func pathToName(src string) string {
	if i := strings.LastIndexAny(src, "/"); i > 0 {
		return src[i+1:]
	}
	return src
}

func (p *Importer) Package(path string) *types.Package {
	const vendorPath = "/vendor/"
	if path == "" {
		return nil
	}
	if index := strings.LastIndex(path, vendorPath); index >= 0 {
		path = path[index+len(vendorPath):]
	}
	pkg := p.packageCache[path]
	if pkg == nil {
		if externalPkg := packages[path]; externalPkg != nil {
			pkg = types.NewPackage(path, externalPkg.Name)
			p.packageCache[path] = pkg
			for _, object := range externalPkg.Objects {
				name := fmt.Sprintf("%s.%s", pkg.Path(), object.Name)
				p.externalObjects[name] = object
				p.newObject(pkg, object)
			}
		} else {
			pkg = types.NewPackage(path, pathToName(path))
			p.packageCache[path] = pkg
		}
		pkg.MarkComplete()
	}
	return pkg
}

func (p *Importer) funcSignature(fun reflect.Type, recv *types.Var, pkg *types.Package) *types.Signature {
	in := make([]*types.Var, fun.NumIn())
	for i := range in {
		param := fun.In(i)
		in[i] = types.NewParam(token.NoPos, p.Package(param.PkgPath()), param.Name(), p.typeOf(param, pkg))
	}
	if recv != nil {
		in = in[1:]
	}
	out := make([]*types.Var, fun.NumOut())
	for i := range out {
		param := fun.Out(i)
		out[i] = types.NewParam(token.NoPos, p.Package(param.PkgPath()), param.Name(), p.typeOf(param, pkg))
	}
	sig := types.NewSignature(recv, types.NewTuple(in...), types.NewTuple(out...), fun.IsVariadic())
	return sig
}

var builtinTypes = map[reflect.Kind]types.BasicKind{
	reflect.Bool:          types.Bool,
	reflect.Int:           types.Int,
	reflect.Int8:          types.Int8,
	reflect.Int16:         types.Int16,
	reflect.Int32:         types.Int32,
	reflect.Int64:         types.Int64,
	reflect.Uint:          types.Uint,
	reflect.Uint8:         types.Uint8,
	reflect.Uint16:        types.Uint16,
	reflect.Uint32:        types.Uint32,
	reflect.Uint64:        types.Uint64,
	reflect.Uintptr:       types.Uintptr,
	reflect.Float32:       types.Float32,
	reflect.Float64:       types.Float64,
	reflect.Complex64:     types.Complex64,
	reflect.Complex128:    types.Complex128,
	reflect.String:        types.String,
	reflect.UnsafePointer: types.UnsafePointer,
}

func (p *Importer) typeOf(t reflect.Type, _ *types.Package) (ttype types.Type) {
	if ttype = p.typeCache[t]; ttype != nil {
		return ttype
	}
	pkg := p.Package(t.PkgPath())
	var namedType *types.Named
	if t.Name() != "" {
		namedType = p.parseNameType(t)
		p.addMethod(t, namedType, pkg)
		SetExternalType(namedType, t)
	}

	switch t.Kind() {
	case reflect.Array:
		ttype = types.NewArray(p.typeOf(t.Elem(), pkg), int64(t.Len()))
	case reflect.Chan:
		var dir types.ChanDir
		switch t.ChanDir() {
		case reflect.RecvDir:
			dir = types.RecvOnly
		case reflect.SendDir:
			dir = types.SendOnly
		case reflect.BothDir:
			dir = types.SendRecv
		}
		ttype = types.NewChan(dir, p.typeOf(t.Elem(), pkg))
	case reflect.Func:
		ttype = p.funcSignature(t, nil, pkg)
	case reflect.Interface:
		methods := make([]*types.Func, t.NumMethod())
		for i := range methods {
			methods[i] = types.NewFunc(token.NoPos, pkg, t.Method(i).Name, p.funcSignature(t.Method(i).Type, nil, pkg))
		}
		ttype = types.NewInterface(methods, nil).Complete()
	case reflect.Map:
		ttype = types.NewMap(p.typeOf(t.Key(), pkg), p.typeOf(t.Elem(), pkg))
	case reflect.Ptr:
		ttype = types.NewPointer(p.typeOf(t.Elem(), pkg))
	case reflect.Slice:
		ttype = types.NewSlice(p.typeOf(t.Elem(), pkg))
	case reflect.Struct:
		fields := make([]*types.Var, 0)
		tags := make([]string, 0)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !ast.IsExported(field.Name) {
				continue
			}
			fields = append(fields, types.NewVar(token.NoPos, p.Package(field.PkgPath), field.Name, p.typeOf(field.Type, pkg)))
			tags = append(tags, string(field.Tag))
		}
		ttype = types.NewStruct(fields, tags)
	case reflect.UnsafePointer:
		ttype = types.Typ[types.UnsafePointer]
	default:
		buildinType, ok := builtinTypes[t.Kind()]
		if ok {
			ttype = types.Typ[buildinType]
		} else {
			ttype = types.Typ[types.Invalid]
		}
	}
	if t.Name() != "" {
		namedType.SetUnderlying(ttype)
		return namedType
	}
	p.typeCache[t] = ttype
	return ttype
}

func (p *Importer) addMethod(t reflect.Type, namedType *types.Named, pkg *types.Package) {
	if t.Kind() != reflect.Interface && t.NumMethod() > 0 {
		recv := types.NewParam(token.NoPos, pkg, "t", namedType)
		for i := 0; i < t.NumMethod(); i++ {
			name := t.Method(i).Name
			sig := p.funcSignature(t.Method(i).Type, recv, pkg)
			fun := types.NewFunc(token.NoPos, pkg, name, sig)
			namedType.AddMethod(fun)
		}
	}
	t = reflect.PtrTo(t)
	if t.NumMethod() > 0 {
		recv := types.NewParam(token.NoPos, pkg, "t", types.NewPointer(namedType))
		for i := 0; i < t.NumMethod(); i++ {
			name := t.Method(i).Name
			sig := p.funcSignature(t.Method(i).Type, recv, pkg)
			fun := types.NewFunc(token.NoPos, pkg, name, sig)
			namedType.AddMethod(fun)
		}
	}
}

func (p *Importer) SsaPackage(name string) *ssa.Package {
	return p.ssaPackages[name]
}

func (p *Importer) parseNameType(t reflect.Type) (named *types.Named) {
	name := t.Name()
	if pkg := p.Package(t.PkgPath()); pkg != nil {
		scope := pkg.Scope()
		if obj := scope.Lookup(name); obj == nil {
			typeName := types.NewTypeName(token.NoPos, pkg, name, nil)
			named = types.NewNamed(typeName, nil, nil)
			scope.Insert(typeName)
			obj = typeName
		} else {
			named = obj.Type().(*types.Named)
		}
	} else {
		typeName := types.NewTypeName(token.NoPos, pkg, name, nil)
		named = types.NewNamed(typeName, nil, nil)
	}
	p.typeCache[t] = named
	return named
}

func (p *Importer) ExternalObject(name string) *ExternalObject {
	return p.externalObjects[name]
}
