package goscript

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"time"

	"github.com/linkxzhou/http_bench/goscript/internal"

	"golang.org/x/tools/go/ssa"
)

var debugging = false

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

func callSSA(caller *frame, fn *ssa.Function, args []internal.Value, env []*internal.Value) internal.Value {
	fr := caller.newChild(fn)
	defer framePool.Put(fr)
	if len(fn.Blocks) > 0 {
		fr.block = fn.Blocks[0]
	}
	for i, l := range fn.Locals {
		fr.locals[i] = zero(deref(l.Type()))
		fr.env[l] = &fr.locals[i]
	}
	for i, p := range fn.Params {
		fr.env[p] = &args[i]
	}
	for i, fv := range fn.FreeVars {
		fr.env[fv] = env[i]
	}
	if fr.block != nil {
		runFrame(fr)
	}
	for i := range fn.Locals {
		fr.locals[i] = nil
	}
	return fr.result
}

func callBuiltin(caller *frame, callPos token.Pos, fn *ssa.Builtin, args []internal.Value) internal.Value {
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
		pos := caller.program.mainPkg.Prog.Fset.Position(callPos)
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

// deref 解引用，若typ为指针类型，返回其指向的类型，否则返回原类型。
func deref(typ types.Type) types.Type {
	if p, ok := typ.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return typ
}

// zero 返回指定类型的零值
func zero(t types.Type) internal.Value {
	v := reflect.New(typeChange(t))
	return internal.RValue{Value: v}
}

// runFrame 在栈帧上执行程序
func runFrame(fr *frame) {
	var instr ssa.Instruction

	defer func() {
		if fr.block == nil {
			return // normal return
		}
		fr.panicking = true
		fr.panic = fmt.Errorf("panic: %s: %v", fr.program.mainPkg.Prog.Fset.Position(instr.Pos()).String(), recover())
		fr.runDefers()
		fr.block = fr.fn.Recover
	}()

	for {
		for _, instr = range fr.block.Instrs {
			fmt.Println("instr: ", instr.String(), reflect.TypeOf(instr))
			c := visitInstr(fr, instr)

			// TODO:
			// if !debugging {
			// 	if err := fr.context.Err(); err != nil {
			// 		panic(err)
			// 	}
			// }

			switch c {
			case _Return:
				return
			case _NEXT:
				// no-op
			case _JUMP:
				break
			}
		}
	}
}

// 下一条执行指令的状态
type nextInstr int

const (
	_NEXT   nextInstr = iota // 继续执行下一条语句
	_Return                  // 函数返回
	_JUMP                    // 跳转到另一个block
)

// visitInstr 执行一条ssa.Instruction语句，返回值nextInstr用于指示下一条语句的位置
func visitInstr(fr *frame, instr ssa.Instruction) nextInstr {
	c := _NEXT
	switch instr := instr.(type) {
	case *ssa.DebugRef:
		// no-op
	case *ssa.Alloc:
		c = runAlloc(fr, instr)
	case *ssa.UnOp:
		c = runUnOp(fr, instr)
	case *ssa.BinOp:
		c = runBinOp(fr, instr)
	case *ssa.MakeInterface:
		c = runMakeInterface(fr, instr)
	case *ssa.Return:
		c = runReturn(fr, instr)
	case *ssa.IndexAddr:
		c = runIndexAddr(fr, instr)
	case *ssa.Field:
		c = runField(fr, instr)
	case *ssa.FieldAddr:
		c = runFieldAddr(fr, instr)
	case *ssa.Store:
		c = runStore(fr, instr)
	case *ssa.Slice:
		c = runSlice(fr, instr)
	case *ssa.Call:
		c = runCall(fr, instr)
	case *ssa.MakeSlice:
		c = runMakeSlice(fr, instr)
	case *ssa.MakeMap:
		c = runMakeMap(fr, instr)
	case *ssa.MapUpdate:
		c = runMapUpdate(fr, instr)
	case *ssa.Lookup:
		c = runLookup(fr, instr)
	case *ssa.Extract:
		c = runExtract(fr, instr)
	case *ssa.If:
		c = runIf(fr, instr)
	case *ssa.Jump:
		c = runJump(fr, instr)
	case *ssa.Phi:
		c = runPhi(fr, instr)
	case *ssa.Convert:
		c = runConvert(fr, instr)
	case *ssa.Range:
		c = runRange(fr, instr)
	case *ssa.Next:
		c = runNext(fr, instr)
	case *ssa.ChangeType:
		c = runChangeType(fr, instr)
	case *ssa.ChangeInterface:
		c = runChangeInterface(fr, instr)
	case *ssa.MakeClosure:
		c = runMakeClosure(fr, instr)
	case *ssa.Defer:
		c = runDefer(fr, instr)
	case *ssa.RunDefers:
		c = runRunDefers(fr, instr)
	case *ssa.MakeChan:
		c = runMakeChan(fr, instr)
	case *ssa.Send:
		c = runSend(fr, instr)
	case *ssa.TypeAssert:
		c = runTypeAssert(fr, instr)
	case *ssa.Go:
		c = runGo(fr, instr)
	case *ssa.Panic:
		c = runPanic(fr, instr)
	case *ssa.Select:
		c = runSelect(fr, instr)
	default:
		panic(fmt.Sprintf("unexpected instruction: %T", instr))
	}

	if debugging {
		fmt.Printf("run %s: \t%s \t%T", fr.program.mainPkg.Prog.Fset.Position(instr.Pos()), instr.String(), instr)
		if val, ok := instr.(ssa.Value); ok {
			v := *fr.env[val]
			if v != nil && v.IsValid() {
				fmt.Printf("\t\t\t%#v", v.Interface()) // debugging
			}
		}
	}

	return c
}
