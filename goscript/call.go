package goscript

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goccy/go-reflect"
	"github.com/linkxzhou/http_bench/goscript/internal"
	"golang.org/x/tools/go/ssa"
)

// upop 一元表达式求值
func unop(instr *ssa.UnOp, x internal.Value) internal.Value {
	if instr.Op == token.MUL {
		return internal.ValueOf(x.Elem().Interface())
	}
	var result interface{}
	switch x.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch instr.Op {
		case token.SUB:
			result = -x.Int()
		case token.XOR:
			result = ^x.Int()
		default:
			panic(fmt.Sprintf("invalid unary op %s %T", instr.Op, x))
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		switch instr.Op {
		case token.SUB:
			result = -x.Uint()
		case token.XOR:
			result = ^x.Uint()
		default:
			panic(fmt.Sprintf("invalid unary op %s %T", instr.Op, x))
		}
	case reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		switch instr.Op {
		case token.SUB:
			result = -x.Float()
		default:
			panic(fmt.Sprintf("invalid unary op %s %T", instr.Op, x))
		}
	case reflect.Bool:
		switch instr.Op {
		case token.NOT:
			result = !x.Bool()
		default:
			panic(fmt.Sprintf("invalid unary op %s %T", instr.Op, x))
		}
	case reflect.Chan: // recv
		v, ok := x.RValue().Recv()
		if !ok {
			v = reflect.Zero(x.Type().Elem())
		}
		if instr.CommaOk {
			return internal.ValueOf([]internal.Value{internal.RValue{Value: v}, internal.ValueOf(ok)})
		}
		return internal.RValue{Value: v}
	}
	return conv(result, instr.Type())
}

// constValue 常量表达式求值
func constValue(c *ssa.Const) internal.Value {
	if c.IsNil() {
		return zero(c.Type()).Elem() // typed nil
	}
	var val interface{}
	t := c.Type().Underlying().(*types.Basic)
	switch t.Kind() {
	case types.Bool, types.UntypedBool:
		val = constant.BoolVal(c.Value)
	case types.Int, types.UntypedInt, types.Int8, types.Int16, types.Int32, types.UntypedRune, types.Int64:
		val = c.Int64()
	case types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
		val = c.Uint64()
	case types.Float32, types.Float64, types.UntypedFloat:
		val = c.Float64()
	case types.Complex64, types.Complex128, types.UntypedComplex:
		val = c.Complex128()
	case types.String, types.UntypedString:
		if c.Value.Kind() == constant.String {
			val = constant.StringVal(c.Value)
		} else {
			val = string(rune(c.Int64()))
		}
	default:
		panic(fmt.Sprintf("constValue: %s", c))
	}
	return conv(val, c.Type())
}

// binop 二元表达式求值
// nolint:gocognit,gocyclo,funlen
func binop(instr *ssa.BinOp, x, y internal.Value) internal.Value {
	var result interface{}
	switch instr.Op {
	case token.ADD: // +
		switch x.Kind() {
		case reflect.String:
			result = x.String() + y.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() + y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() + y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() + y.Uint()
		}

	case token.SUB: // -
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() - y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() - y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() - y.Uint()
		}

	case token.MUL: // *
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() * y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() * y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() * y.Uint()
		}

	case token.QUO: // /
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() / y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() / y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() / y.Uint()
		}

	case token.REM: // %
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() % y.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() % y.Uint()
		}

	case token.AND: // &
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() & y.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() & y.Uint()
		}

	case token.OR: // |
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() | y.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() | y.Uint()
		}

	case token.XOR: // ^
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() ^ y.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() ^ y.Uint()
		}

	case token.AND_NOT: // &^
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() &^ y.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() &^ y.Uint()
		}

	case token.SHL: // <<
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() << y.Uint()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() << y.Uint()
		}

	case token.SHR: // >>
		switch x.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() >> y.Uint()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() >> y.Uint()
		}

	case token.LSS: // <
		switch x.Kind() {
		case reflect.String:
			result = x.String() < y.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() < y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() < y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() < y.Uint()
		}

	case token.LEQ: // <=
		switch x.Kind() {
		case reflect.String:
			result = x.String() <= y.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() <= y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() <= y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() <= y.Uint()
		}

	case token.EQL: // ==
		if x.IsNil() || y.IsNil() {
			result = x.IsNil() && y.IsNil()
		} else {
			result = x.Interface() == y.Interface()
		}

	case token.NEQ: // !=
		if x.IsNil() || y.IsNil() {
			result = x.IsNil() != y.IsNil()
		} else {
			result = x.Interface() != y.Interface()
		}

	case token.GTR: // >
		switch x.Kind() {
		case reflect.String:
			result = x.String() > y.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() > y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() > y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() > y.Uint()
		}

	case token.GEQ: // >=
		switch x.Kind() {
		case reflect.String:
			result = x.String() >= y.String()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			result = x.Int() >= y.Int()
		case reflect.Float32, reflect.Float64:
			result = x.Float() >= y.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			result = x.Uint() >= y.Uint()
		}
	}

	return conv(result, instr.Type())
}

// goCall go语句执行
func goCall(state *State, instr *ssa.CallCommon) {
	if instr.Signature().Recv() != nil {
		recv := state.get(instr.Args[0])
		if recv.RValue().NumMethod() > 0 { // external method
			args := state.GetValue(len(instr.Args) - 1)
			for i := range args {
				args[i] = state.get(instr.Args[i+1])
			}
			go callExternal(recv.RValue().MethodByName(instr.Value.Name()), args)
			return
		}
	}

	args := state.GetValue(len(instr.Args))
	for i, arg := range instr.Args {
		args[i] = state.get(arg)
	}

	atomic.AddInt32(&state.context.goroutines, 1)

	go func(caller *State, fn ssa.Value, args []internal.Value) {
		defer func() {
			// 启动协程前添加recover语句，避免协程panic影响其他协程
			if re := recover(); re != nil {
				caller.context.outBuffer.WriteString(fmt.Sprintf("goroutine panic: %v", re))
			}
			atomic.AddInt32(&caller.context.goroutines, -1)
		}()
		call(caller, instr.Pos(), fn, args)
	}(state, instr.Value, args)
}

// callOp 函数调用语句执行
func callOp(state *State, instr *ssa.CallCommon) internal.Value {
	if instr.Signature().Recv() == nil {
		// call func
		args := state.GetValue(len(instr.Args))
		for i, arg := range instr.Args {
			args[i] = state.get(arg)
		}
		return call(state, instr.Pos(), instr.Value, args)
	}

	// invoke Method
	if instr.IsInvoke() {
		recv := state.get(instr.Value)
		args := state.GetValue(len(instr.Args))
		for i := range args {
			args[i] = state.get(instr.Args[i])
		}
		return callExternal(recv.RValue().MethodByName(instr.Method.Name()), args)
	}

	args := state.GetValue(len(instr.Args))
	for i, arg := range instr.Args {
		args[i] = state.get(arg)
	}
	if args[0].Type().NumMethod() == 0 {
		return call(state, instr.Pos(), instr.Value, args)
	}
	return callExternal(args[0].RValue().MethodByName(instr.Value.Name()), args[1:])
}

// call 函数调用
func call(state *State, callpos token.Pos, fn interface{}, args []internal.Value) internal.Value {
	switch fun := fn.(type) {
	case *ssa.Function:
		if fun == nil {
			panic("call of nil function") // nil of func type
		}
		return callSSA(state, fun, args, nil)
	case *ssa.Builtin:
		return callBuiltin(state, callpos, fun, args)
	case *internal.ExternalValue:
		return callExternal(fun.Object.Value, args)
	case ssa.Value:
		p := state.env[fun]
		f := p.Interface()
		return call(state, callpos, f, args)
	default:
		return callExternal(reflect.ValueOf(fun), args)
	}
}

func callExternal(fn reflect.Value, args []internal.Value) internal.Value {
	fnType := fn.Type()
	numIn := fnType.NumIn()
	if fnType.IsVariadic() {
		numIn--
	}
	in := make([]reflect.Value, numIn)
	for i := 0; i < numIn; i++ {
		in[i] = args[i].RValue().Convert(fnType.In(i))
	}
	if fnType.IsVariadic() {
		variadicType := fnType.In(numIn).Elem()
		variadicArgs := args[len(args)-1]
		variadicLen := variadicArgs.Len()
		for i := 0; i < variadicLen; i++ {
			in = append(in, variadicArgs.Index(i).RValue().Convert(variadicType))
		}
	}
	out := fn.Call(in)
	return internal.Package(out)
}

func callSSA(caller *State, fn *ssa.Function, args []internal.Value, env []internal.Value) internal.Value {
	state := caller.newChild(fn)
	defer func() {
		state.PutValueAll()
		statePool.Put(state)
	}()
	if len(fn.Blocks) > 0 {
		state.block = fn.Blocks[0]
	}
	for i, l := range fn.Locals {
		state.locals[i] = zero(deref(l.Type()))
		state.env[l] = state.locals[i]
	}
	for i, p := range fn.Params {
		state.env[p] = args[i]
	}
	for i, fv := range fn.FreeVars {
		state.env[fv] = env[i]
	}
	if state.block != nil {
		ssaStack(state)
	}
	for i := range fn.Locals {
		state.locals[i] = nil
	}
	return state.result
}

func callBuiltin(caller *State, callPos token.Pos, fn *ssa.Builtin, args []internal.Value) internal.Value {
	switch fn.Name() {
	case "append":
		if args[1].RValue().IsNil() {
			return args[0]
		}
		elems := make([]reflect.Value, args[1].Elem().Len())
		for i := range elems {
			elems[i] = args[1].RValue().Index(i)
		}
		return internal.RValue{Value: reflect.Append(args[0].RValue(), elems...)}

	case "copy":
		reflect.Copy(args[0].RValue(), args[1].RValue())

	case "close": // close(chan T)
		args[0].RValue().Close()
		return nil

	case "delete": // delete(map[K]value, K)
		args[0].RValue().SetMapIndex(args[1].RValue(), reflect.Value{})
		return nil

	case "print", "println": // print(any, ...)
		buf := &caller.context.outBuffer
		s := make([]string, len(args))
		for i, arg := range args {
			s[i] = fmt.Sprint(arg.Interface())
		}
		pos := caller.program.MainPkg.Prog.Fset.Position(callPos)
		buf.WriteString(fmt.Sprintf("[%s %s:%d] %s\n",
			time.Now().Format("15:04:05"),
			pos.Filename, pos.Line,
			strings.Join(s, " "),
		))
		return nil

	case "len":
		return internal.ValueOf(args[0].Len())

	case "cap":
		return internal.ValueOf(args[0].Cap())

	case "panic":
		panic(args[0].Interface())

	case "recover":
		if caller.caller.panicking {
			caller.caller.panicking = false
			return internal.ValueOf(caller.caller.panic)
		}
		return internal.ValueOf(recover())
	}
	panic("unknown built-in: " + fn.Name())
}
