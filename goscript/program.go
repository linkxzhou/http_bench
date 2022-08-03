package goscript

import (
	"errors"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"go/types"
	"os"

	"github.com/linkxzhou/http_bench/goscript/internal"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

type Program struct {
	mainPkg   *ssa.Package
	globals   map[ssa.Value]*internal.Value
	importPkg []string
}

func ParseFuncList(sourceCode string, exportedAll bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", sourceCode, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var flist []string
	for _, v := range f.Decls {
		switch t := v.(type) {
		case *ast.FuncDecl:
			if f, ok := v.(*ast.FuncDecl); !ok || f.Recv != nil {
				continue
			}
			if exportedAll {
				flist = append(flist, t.Name.Name)
			} else {
				if t.Name.IsExported() && t.Name.Name != "init" {
					flist = append(flist, t.Name.Name)
				}
			}
		default:
			// pass
		}
	}
	return flist, nil
}

func Run(sourceCode string, funcName string, params ...interface{}) (interface{}, error) {
	if program, err := BuildProgram("main", sourceCode); err != nil {
		return nil, err
	} else {
		return program.Run(funcName, params...)
	}
}

func autoImport(f *ast.File) []string {
	var importPkg []string
	imported := make(map[string]bool)
	for _, i := range f.Imports {
		imported[i.Path.Value] = true
		importPkg = append(importPkg, i.Path.Value)
	}
	for _, unresolved := range f.Unresolved {
		if doc.IsPredeclared(unresolved.Name) {
			continue
		}
		if importSpec := internal.GetPackageByName(unresolved.Name); importSpec != nil {
			if imported[importSpec.Path.Value] {
				continue
			}
			imported[importSpec.Path.Value] = true
			f.Imports = append(f.Imports, importSpec)
			f.Decls = append(f.Decls, &ast.GenDecl{
				Specs: []ast.Spec{importSpec},
			})
		}
	}
	return importPkg
}

func BuildProgram(fname, sourceCode string, packages ...*ssa.Package) (*Program, error) {
	fset := token.NewFileSet()
	// ParseFile {fname}.go
	f, err := parser.ParseFile(fset, fname+".go", sourceCode, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	files := []*ast.File{f}

	importPkg := autoImport(f)
	pkg := types.NewPackage(f.Name.Name, f.Name.Name)

	packageImporter := internal.NewImporter(packages...)
	mode := ssa.SanityCheckFunctions | ssa.BareInits
	mainPkg, _, err := ssautil.BuildPackage(&types.Config{Importer: packageImporter}, fset, pkg, files, mode)
	if err != nil {
		return nil, err
	}
	program := &Program{
		mainPkg:   mainPkg,
		globals:   make(map[ssa.Value]*internal.Value),
		importPkg: importPkg,
	}
	internal.ExternalValueWrap(packageImporter, mainPkg)
	program.initGlobal()
	context := newCallContext()
	fr := &frame{program: program, context: context}
	if init := mainPkg.Func("init"); init != nil {
		for _, pkg := range packages {
			if dependInit := pkg.Func("init"); dependInit != nil {
				callSSA(fr, dependInit, nil, nil)
			}
		}
		callSSA(fr, init, nil, nil)
	}
	context.cancelFunc()
	return program, nil
}

func (p *Program) Run(funcName string, params ...interface{}) (interface{}, error) {
	val, _, err := p.RunWithContext(funcName, params...)
	return val, err
}

func (p *Program) RunWithContext(funcName string, params ...interface{}) (result interface{}, ctx *Context, err error) {
	defer func() {
		if re := recover(); re != nil {
			err = fmt.Errorf("%v", re)
		}
	}()
	ctx = newCallContext()
	mainFn := p.mainPkg.Func(funcName)
	if mainFn == nil {
		return nil, nil, errors.New("function not found")
	}
	if debugging {
		mainFn.WriteTo(os.Stdout)
	}
	args := make([]internal.Value, len(params))
	for i := range args {
		args[i] = internal.ValueOf(params[i])
	}
	fr := &frame{
		program: p,
		context: ctx,
	}
	if ret := callSSA(fr, mainFn, args, nil); ret != nil {
		result = ret.Interface()
	}
	ctx.cancelFunc()
	return
}

func (p *Program) initGlobal() {
	for _, v := range p.mainPkg.Members {
		if g, ok := v.(*ssa.Global); ok {
			global := zero(g.Type().(*types.Pointer).Elem()).Elem()
			p.globals[g] = &global
		}
	}
}

func (p *Program) SetGlobalValue(name string, val interface{}) error {
	v := p.mainPkg.Members[name]
	if g, ok := v.(*ssa.Global); ok {
		global := internal.ValueOf(val)
		p.globals[g] = &global
		return nil
	}
	return fmt.Errorf("global Value %s not found", name)
}

func (p *Program) Package() *ssa.Package {
	return p.mainPkg
}
