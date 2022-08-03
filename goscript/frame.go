package goscript

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/linkxzhou/http_bench/goscript/internal"

	"golang.org/x/tools/go/ssa"
)

const defaultTimeout = 10 * time.Second

var framePool = &sync.Pool{
	New: func() interface{} {
		return &frame{}
	},
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

type frame struct {
	program          *Program
	caller           *frame
	fn               *ssa.Function
	block, prevBlock *ssa.BasicBlock
	env              map[ssa.Value]*internal.Value
	locals           []internal.Value
	defers           []*ssa.Defer
	result           internal.Value
	panicking        bool
	panic            interface{}

	context *Context
}

func (fr *frame) makeFunc(f *ssa.Function, bindings []ssa.Value) internal.Value {
	env := make([]*internal.Value, len(bindings))
	for i, binding := range bindings {
		env[i] = fr.env[binding]
	}
	in := make([]reflect.Type, len(f.Params))
	for i, param := range f.Params {
		in[i] = typeChange(param.Type())
	}
	out := make([]reflect.Type, 0)
	results := f.Signature.Results()
	for i := 0; i < results.Len(); i++ {
		out = append(out, typeChange(results.At(i).Type()))
	}
	funcType := reflect.FuncOf(in, out, f.Signature.Variadic())
	fn := func(in []reflect.Value) (results []reflect.Value) {
		args := make([]internal.Value, len(in))
		for i, arg := range in {
			args[i] = internal.RValue{Value: arg}
		}
		if ret := callSSA(fr, f, args, env); ret != nil {
			return internal.Unpackage(ret)
		} else {
			return nil
		}
	}
	return internal.RValue{Value: reflect.MakeFunc(funcType, fn)}
}

func (fr *frame) get(key ssa.Value) internal.Value {
	switch key := key.(type) {
	case nil:
		return nil
	case *ssa.Const:
		return constValue(key)
	case *ssa.Global:
		if r, ok := fr.program.globals[key]; ok {
			v := (*r).Interface()
			return internal.ValueOf(&v)
		}
	case *internal.ExternalValue:
		return key.ToValue()
	case *ssa.Function:
		return fr.makeFunc(key, nil)
	}
	if r, ok := fr.env[key]; ok {
		return *r
	}
	panic(fmt.Sprintf("get: no Value for %T: %v", key, key.Name()))
}

func (fr *frame) set(instr ssa.Value, value internal.Value) {
	fr.env[instr] = &value
}

func (fr *frame) newChild(fn *ssa.Function) *frame {
	f := framePool.Get().(*frame)
	f.program = fr.program
	f.context = fr.context
	f.caller = fr // for panic/recover
	f.fn = fn
	if f.env == nil {
		f.env = make(map[ssa.Value]*internal.Value)
	}
	if f.locals == nil {
		f.locals = make([]internal.Value, len(fn.Locals))
	}
	return f
}

func (fr *frame) runDefers() {
	for i := len(fr.defers) - 1; i >= 0; i-- {
		fr.runDefer(fr.defers[i])
	}
	fr.defers = nil
	if fr.panicking {
		panic(fr.panic)
	}
}

func (fr *frame) runDefer(d *ssa.Defer) {
	var ok bool
	defer func() {
		if !ok {
			fr.panicking = true
			fr.panic = recover()
		}
	}()
	callOp(fr, d.Common())
	ok = true
}
