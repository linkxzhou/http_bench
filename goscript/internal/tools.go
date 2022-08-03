package internal

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/token"
	"go/types"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var sourceDir string

var importPkgs = map[string][]string{
	"bytes":           []string{},
	"container/heap":  []string{},
	"container/list":  []string{},
	"container/ring":  []string{},
	"crypto/md5":      []string{},
	"encoding/base64": []string{},
	"encoding/hex":    []string{},
	"encoding/xml":    []string{},
	"errors":          []string{},
	"fmt":             []string{},
	"html":            []string{},
	"math":            []string{},
	"math/rand":       []string{},
	"net/http":        []string{},
	"net/url":         []string{},
	"regexp":          []string{},
	"sort":            []string{},
	"strconv":         []string{},
	"strings":         []string{},
	"time":            []string{},
	"unicode":         []string{},
	"unicode/utf8":    []string{},
	"unicode/utf16":   []string{},
	"sync":            []string{},
	"sync/atomic":     []string{},

	"crypto/sha1":     []string{},
	"encoding/json":   []string{},
	"encoding/binary": []string{},
	"io/ioutil":       []string{"io"},
	"io":              []string{},
	"html/template":   []string{},
	"path":            []string{},
	"mime/multipart":  []string{},
	"crypto/des":      []string{},
	"crypto/cipher":   []string{},
	"crypto/tls":      []string{},
}

func init() {
	_, filename, _, _ := runtime.Caller(0)
	sourceDir = filepath.Dir(filename)
}

func main() {
	for path, vlist := range importPkgs {
		fmt.Println("path: ", path, ", vlist: ", vlist)
		err := packageImport(path, vlist)
		if err != nil {
			println(path, err.Error())
			continue
		}
	}
}

func objectDecl(object types.Object) string {
	name := fmt.Sprintf("%s.%s", object.Pkg().Name(), object.Name())

	switch object.(type) {
	case *types.TypeName:
		return fmt.Sprintf(`register.NewType("%s", reflect.TypeOf(func(%s){}).In(0), "%s")`, object.Name(), name, "")
	case *types.Const:
		if object.Name() == "MaxUint64" {
			name = fmt.Sprintf("uint(%s)", name)
		}
		return fmt.Sprintf(`register.NewConst("%s", %s, "%s")`, object.Name(), name, "")
	case *types.Var:
		switch object.Type().Underlying().(type) {
		case *types.Interface:
			return fmt.Sprintf(`register.NewVar("%s", &%s, reflect.TypeOf(func (%s){}).In(0), "%s")`, object.Name(), name, trimVendor(object.Type().String()), "")
		default:
			return fmt.Sprintf(`register.NewVar("%s", &%s, reflect.TypeOf(%s), "%s")`, object.Name(), name, name, "")
		}

	case *types.Func:
		return fmt.Sprintf(`register.NewFunction("%s", %s, "%s")`, object.Name(), name, "")
	}
	return ""
}

func trimVendor(src string) string {
	if i := strings.LastIndex(src, `vendor/`); i >= 0 {
		return src[i+7:]
	}
	return src
}

func packageImport(path string, vlist []string) error {
	pkg, err := importer.ForCompiler(token.NewFileSet(), "source", nil).Import(path)
	if err != nil {
		return err
	}

	preImports := ""
	for _, v := range vlist {
		preImports = preImports + `"` + v + `"` + "\n"
	}

	builder := strings.Builder{}
	pkgPath := trimVendor(pkg.Path())
	fmt.Println("pkg.Path(): ", pkg.Path(), ", pkgPath: ", pkgPath)
	builder.WriteString(fmt.Sprintf(`package imports
import (
	%s
	"%s"
	"reflect"
)

var _ = reflect.Int

func init() {
	register.AddPackage("%s", "%s",`+"\n", preImports, path, path, pkg.Name()))
	scope := pkg.Scope()
	for _, declName := range pkg.Scope().Names() {
		if ast.IsExported(declName) {
			obj := scope.Lookup(declName)
			builder.WriteString(strings.Replace(objectDecl(obj), path, pkg.Name(), 1) + ",\n")
		}
	}
	builder.WriteString(`)
}`)

	src := builder.String()
	code, err := format.Source([]byte(src))
	if err != nil {
		code = []byte(src)
		println(path, err.Error())
	}
	fmt.Println("pkg.Name(): ", pkg.Name())
	filename := fmt.Sprintf("%s%c%s.go", filepath.Dir(sourceDir), os.PathSeparator, pkg.Name())
	return ioutil.WriteFile(filename, code, 0666)
}
