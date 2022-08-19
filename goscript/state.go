package goscript

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go/types"

	"github.com/goccy/go-reflect"
	"github.com/linkxzhou/http_bench/goscript/internal"
	"golang.org/x/tools/go/ssa"
)

const defaultTimeout = 10 * time.Second

var statePool = &sync.Pool{
	New: func() interface{} {
		state := &State{}
		state.slab.Init()
		return state
	},
}

var builtinTypes = map[types.BasicKind]reflect.Type{
	types.Bool:       reflect.TypeOf(true),
	types.Int:        reflect.TypeOf(int(0)),
	types.Int8:       reflect.TypeOf(int8(0)),
	types.Int16:      reflect.TypeOf(int16(0)),
	types.Int32:      reflect.TypeOf(int32(0)),
	types.Int64:      reflect.TypeOf(int64(0)),
	types.Uint:       reflect.TypeOf(uint(0)),
	types.Uint8:      reflect.TypeOf(uint8(0)),
	types.Uint16:     reflect.TypeOf(uint16(0)),
	types.Uint32:     reflect.TypeOf(uint32(0)),
	types.Uint64:     reflect.TypeOf(uint64(0)),
	types.Uintptr:    reflect.TypeOf(uintptr(0)),
	types.Float32:    reflect.TypeOf(float32(0)),
	types.Float64:    reflect.TypeOf(float64(0)),
	types.Complex64:  reflect.TypeOf(complex64(0)),
	types.Complex128: reflect.TypeOf(complex128(0)),
	types.String:     reflect.TypeOf(""),

	types.UntypedBool:    reflect.TypeOf(true),
	types.UntypedInt:     reflect.TypeOf(int(0)),
	types.UntypedRune:    reflect.TypeOf(rune(0)),
	types.UntypedFloat:   reflect.TypeOf(float64(0)),
	types.UntypedComplex: reflect.TypeOf(complex128(0)),
	types.UntypedString:  reflect.TypeOf(""),
}

type Context struct {
	context.Context
	outBuffer  strings.Builder
	goroutines int32
	cancelFunc context.CancelFunc
}

func (p *Context) Output() string {
	return p.outBuffer.String()
}

func newCallContext() *Context {
	ctx, cancelFunc := context.WithTimeout(context.Background(), defaultTimeout)
	return &Context{
		Context:    ctx,
		cancelFunc: cancelFunc,
	}
}

type State struct {
	program          *Program
	caller           *State
	fn               *ssa.Function
	block, prevBlock *ssa.BasicBlock
	env              map[ssa.Value]internal.Value
	locals           []internal.Value
	defers           []*ssa.Defer
	result           internal.Value
	panicking        bool
	panic            interface{}
	debugging        bool
	slab             internal.ValueSlab

	context *Context
}

func (s *State) GetValue(size int) []internal.Value {
	return s.slab.Get(size)
}

func (s *State) PutValueAll() bool {
	return s.slab.PutAll()
}

func (s *State) makeFunc(f *ssa.Function, bindings []ssa.Value) internal.Value {
	env := s.GetValue(len(bindings))
	for i, binding := range bindings {
		env[i] = s.env[binding]
	}
	in := make([]reflect.Type, len(f.Params))
	for i, param := range f.Params {
		in[i] = typeCopy(param.Type())
	}
	out := make([]reflect.Type, 0)
	results := f.Signature.Results()
	for i := 0; i < results.Len(); i++ {
		out = append(out, typeCopy(results.At(i).Type()))
	}
	funcType := reflect.FuncOf(in, out, f.Signature.Variadic())
	fn := func(in []reflect.Value) (results []reflect.Value) {
		args := s.GetValue(len(in))
		for i, arg := range in {
			args[i] = internal.RValue{Value: arg}
		}
		if ret := callSSA(s, f, args, env); ret != nil {
			return internal.Unpackage(ret)
		} else {
			return nil
		}
	}
	return internal.RValue{Value: reflect.MakeFunc(funcType, fn)}
}

func (s *State) get(key ssa.Value) internal.Value {
	switch key := key.(type) {
	case nil:
		return nil
	case *ssa.Const:
		return constValue(key)
	case *ssa.Global:
		if r, ok := s.program.globals[key]; ok {
			v := (*r).Interface()
			return internal.ValueOf(&v)
		}
	case *internal.ExternalValue:
		return key.ToValue()
	case *ssa.Function:
		return s.makeFunc(key, nil)
	}
	if r, ok := s.env[key]; ok {
		return r
	}
	panic(fmt.Sprintf("get: no Value for %T: %v", key, key.Name()))
}

func (s *State) set(instr ssa.Value, value internal.Value) {
	s.env[instr] = value
}

func (s *State) newChild(fn *ssa.Function) *State {
	f := statePool.Get().(*State)
	f.program = s.program
	f.context = s.context
	f.caller = s // for panic/recover
	f.fn = fn
	f.env = make(map[ssa.Value]internal.Value)
	f.locals = s.GetValue(len(fn.Locals))
	return f
}

func (s *State) callDefers() {
	for i := len(s.defers) - 1; i >= 0; i-- {
		s.callDefer(s.defers[i])
	}
	s.defers = nil
	if s.panicking {
		panic(s.panic)
	}
}

func (s *State) callDefer(d *ssa.Defer) {
	var ok bool
	defer func() {
		if !ok {
			s.panicking = true
			s.panic = recover()
		}
	}()
	callOp(s, d.Common())
	ok = true
}

func typeCopy(typ types.Type) reflect.Type {
	rType := internal.GetCacheType(typ)
	if rType != nil {
		return rType
	}
	switch t := typ.Underlying().(type) {
	case *types.Array:
		rType = reflect.ArrayOf(int(t.Len()), typeCopy(t.Elem()))
	case *types.Basic:
		rtype := builtinTypes[t.Kind()]
		if rtype == nil {
			panic(t.Kind())
		}
		rType = rtype
	case *types.Chan:
		var dir reflect.ChanDir
		switch t.Dir() {
		case types.RecvOnly:
			dir = reflect.RecvDir
		case types.SendOnly:
			dir = reflect.SendDir
		case types.SendRecv:
			dir = reflect.BothDir
		default:
			// pass
		}
		rType = reflect.ChanOf(dir, typeCopy(t.Elem()))
	case *types.Interface:
		rType = reflect.TypeOf(func(interface{}) {}).In(0)
	case *types.Map:
		rType = reflect.MapOf(typeCopy(t.Key()), typeCopy(t.Elem()))
	case *types.Pointer:
		rType = reflect.PtrTo(typeCopy(t.Elem()))
	case *types.Slice:
		rType = reflect.SliceOf(typeCopy(t.Elem()))
	case *types.Struct:
		fields := make([]reflect.StructField, t.NumFields())
		for i := range fields {
			field := t.Field(i)
			fields[i] = reflect.StructField{
				Name:      field.Name(),
				Type:      typeCopy(t.Field(i).Type()),
				Tag:       reflect.StructTag(t.Tag(i)),
				Offset:    0,
				Index:     []int{i},
				Anonymous: field.Anonymous(),
			}
		}
		rType = reflect.StructOf(fields)
	default:
		rType = reflect.TypeOf(func(interface{}) {}).In(0)
	}
	return rType
}

func conv(v interface{}, typ types.Type) internal.Value {
	rtype := typeCopy(typ)
	if v == nil {
		return internal.RValue{Value: reflect.Zero(rtype)}
	}
	reflectValue := reflect.ValueOf(v).Convert(rtype)
	return internal.RValue{Value: reflectValue}
}
