package goscript

import (
	"fmt"
	"go/types"
	"reflect"

	"github.com/linkxzhou/http_bench/goscript/internal"
	"golang.org/x/tools/go/ssa"
)

func runAlloc(fr *frame, instr *ssa.Alloc) nextInstr {
	var addr *internal.Value
	if instr.Heap {
		addr = new(internal.Value) // 堆分配
		fr.env[instr] = addr
	} else {
		addr = fr.env[instr] // 栈分配
	}
	*addr = zero(deref(instr.Type()))
	return _NEXT
}

func runUnOp(fr *frame, instr *ssa.UnOp) nextInstr {
	v := unop(instr, fr.get(instr.X))
	fr.set(instr, v)
	return _NEXT
}

func runBinOp(fr *frame, instr *ssa.BinOp) nextInstr {
	v := binop(instr, fr.get(instr.X), fr.get(instr.Y))
	fr.set(instr, v)
	return _NEXT
}

func runMakeInterface(fr *frame, instr *ssa.MakeInterface) nextInstr {
	v := fr.get(instr.X)
	fr.set(instr, v)
	return _NEXT
}

func runReturn(fr *frame, instr *ssa.Return) nextInstr {
	switch len(instr.Results) {
	case 0:
	case 1:
		fr.result = fr.get(instr.Results[0])
	default:
		// 返回多值时，对返回值进行打包
		var res []internal.Value
		for _, r := range instr.Results {
			res = append(res, fr.get(r))
		}
		fr.result = internal.ValueOf(res)
	}
	fr.block = nil
	return _Return
}

func runIndexAddr(fr *frame, instr *ssa.IndexAddr) nextInstr {
	x := fr.get(instr.X)
	idx := int(fr.get(instr.Index).Int())
	fr.set(instr, internal.RValue{Value: x.Elem().RValue().Index(idx).Addr()})
	return _NEXT
}

func runField(fr *frame, instr *ssa.Field) nextInstr {
	x := fr.get(instr.X)
	fr.set(instr, x.Field(instr.Field))
	return _NEXT
}

func runFieldAddr(fr *frame, instr *ssa.FieldAddr) nextInstr {
	x := fr.get(instr.X).Elem()
	v := x.RValue().Field(instr.Field).Addr()
	fr.set(instr, internal.RValue{Value: v})
	return _NEXT
}

func runStore(fr *frame, instr *ssa.Store) nextInstr {
	// Store指令需要根据目标的类型进行不同的操作
	switch addr := instr.Addr.(type) {
	case *ssa.Alloc, *ssa.FreeVar:
		// 局部变量
		v := fr.get(instr.Val)
		(*fr.env[addr]).Elem().Set(v)
	case *ssa.Global:
		// 全局变量
		v := fr.get(instr.Val)
		fr.program.globals[addr] = &v
	case *internal.ExternalValue:
		// 外部变量
		v := fr.get(instr.Val)
		addr.Store(v)
	case *ssa.IndexAddr:
		// 下标表达式
		index := int(fr.get(addr.Index).Int())
		x := fr.get(addr.X).Elem()
		val := fr.get(instr.Val)
		x.Index(index).Set(val)
	default:
		// 根据地址（指针）赋值
		v := *fr.env[addr]
		v.Elem().Set(fr.get(instr.Val))
	}
	return _NEXT
}

func runSlice(fr *frame, instr *ssa.Slice) nextInstr {
	x := fr.get(instr.X).Elem()
	l, h := 0, x.Len()
	low := fr.get(instr.Low)
	if low != nil {
		l = int(low.Int())
	}
	high := fr.get(instr.High)
	if high != nil {
		h = int(high.Int())
	}
	max := fr.get(instr.Max)
	if max != nil {
		fr.set(instr, internal.RValue{Value: x.RValue().Slice3(l, h, int(max.Int()))})
	} else {
		fr.set(instr, internal.RValue{Value: x.RValue().Slice(l, h)})
	}
	return _NEXT
}

func runCall(fr *frame, instr *ssa.Call) nextInstr {
	if v := callOp(fr, instr.Common()); v != nil {
		fr.env[instr] = &v
	}
	return _NEXT
}

func runMakeSlice(fr *frame, instr *ssa.MakeSlice) nextInstr {
	sliceLen := int(fr.get(instr.Len).Int())
	sliceCap := int(fr.get(instr.Cap).Int())
	fr.set(instr, internal.RValue{Value: reflect.MakeSlice(typeChange(instr.Type()), sliceLen, sliceCap)})
	return _NEXT
}

func runMakeMap(fr *frame, instr *ssa.MakeMap) nextInstr {
	fr.set(instr, internal.RValue{Value: reflect.MakeMap(typeChange(instr.Type()))})
	return _NEXT
}

func runMapUpdate(fr *frame, instr *ssa.MapUpdate) nextInstr {
	m := fr.get(instr.Map)
	key := fr.get(instr.Key)
	v := fr.get(instr.Value)
	m.Elem().RValue().SetMapIndex(key.RValue(), v.RValue())
	return _NEXT
}

func runLookup(fr *frame, instr *ssa.Lookup) nextInstr {
	x := fr.get(instr.X)
	index := fr.get(instr.Index)
	if x.Type().Kind() == reflect.Map {
		v := x.MapIndex(index)
		ok := true
		if !v.IsValid() {
			v = internal.RValue{Value: reflect.Zero(x.Type().Elem())}
			ok = false
		}
		if instr.CommaOk {
			v = internal.ValueOf([]internal.Value{v, internal.ValueOf(ok)})
		}
		fr.set(instr, v)
	} else {
		fr.set(instr, x.Index(int(index.Int())))
	}
	return _NEXT
}

func runExtract(fr *frame, instr *ssa.Extract) nextInstr {
	fr.set(instr, fr.get(instr.Tuple).Index(instr.Index).Interface().(internal.Value))
	return _NEXT
}

func runIf(fr *frame, instr *ssa.If) nextInstr {
	succ := 1
	if fr.get(instr.Cond).Bool() {
		succ = 0
	}
	fr.prevBlock, fr.block = fr.block, fr.block.Succs[succ]
	return _JUMP
}

func runJump(fr *frame, instr *ssa.Jump) nextInstr {
	fr.prevBlock, fr.block = fr.block, fr.block.Succs[0]
	return _JUMP
}

func runPhi(fr *frame, instr *ssa.Phi) nextInstr {
	for i, pred := range instr.Block().Preds {
		if fr.prevBlock == pred {
			fr.set(instr, fr.get(instr.Edges[i]))
			break
		}
	}
	return _NEXT
}

func runConvert(fr *frame, instr *ssa.Convert) nextInstr {
	fr.set(instr, conv(fr.get(instr.X).Interface(), instr.Type()))
	return _NEXT
}

func runRange(fr *frame, instr *ssa.Range) nextInstr {
	v := fr.get(instr.X)
	fr.set(instr, &internal.MapIter{
		I:     0,
		Value: v,
		Keys:  v.RValue().MapKeys(),
	})
	return _NEXT
}

func runNext(fr *frame, instr *ssa.Next) nextInstr {
	fr.set(instr, fr.get(instr.Iter).Next())
	return _NEXT
}

func runChangeType(fr *frame, instr *ssa.ChangeType) nextInstr {
	fr.set(instr, fr.get(instr.X))
	return _NEXT
}

func runChangeInterface(fr *frame, instr *ssa.ChangeInterface) nextInstr {
	fr.set(instr, fr.get(instr.X))
	return _NEXT
}

func runMakeClosure(fr *frame, instr *ssa.MakeClosure) nextInstr {
	closure := fr.makeFunc(instr.Fn.(*ssa.Function), instr.Bindings)
	fr.set(instr, closure)
	return _NEXT
}

func runDefer(fr *frame, instr *ssa.Defer) nextInstr {
	fr.defers = append(fr.defers, instr)
	return _NEXT
}

func runRunDefers(fr *frame, instr *ssa.RunDefers) nextInstr {
	fr.runDefers()
	return _NEXT
}

func runMakeChan(fr *frame, instr *ssa.MakeChan) nextInstr {
	fr.set(instr, internal.RValue{Value: reflect.MakeChan(typeChange(instr.Type()), int(fr.get(instr.Size).Int()))})
	return _NEXT
}

func runSend(fr *frame, instr *ssa.Send) nextInstr {
	fr.get(instr.Chan).RValue().Send(fr.get(instr.X).RValue())
	return _NEXT
}

func runTypeAssert(fr *frame, instr *ssa.TypeAssert) nextInstr {
	v := fr.get(instr.X)
	for v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	destType := typeChange(instr.AssertedType)

	var assignable bool
	if v.Kind() == reflect.Invalid {
		assignable = false
	} else {
		assignable = v.Type().AssignableTo(destType)
	}

	switch {
	case instr.CommaOk && assignable:
		fr.set(instr, internal.ValueOf([]internal.Value{v, internal.ValueOf(true)}))

	case instr.CommaOk && !assignable:
		fr.set(instr, internal.ValueOf([]internal.Value{internal.RValue{Value: reflect.Zero(destType)}, internal.ValueOf(false)}))

	case !instr.CommaOk && assignable:
		fr.set(instr, v)

	case !instr.CommaOk && !assignable:
		if v.Kind() == reflect.Invalid {
			panic(fmt.Errorf("interface conversion: interface is nil, not %s", destType.String()))
		} else {
			panic(fmt.Errorf("interface conversion: interface is %s, not %s", v.Type().String(), destType.String()))
		}
	}
	return _NEXT
}

func runGo(fr *frame, instr *ssa.Go) nextInstr {
	goCall(fr, instr.Common())
	return _NEXT
}

func runPanic(fr *frame, instr *ssa.Panic) nextInstr {
	panic(fr.get(instr.X).Interface())
}

func runSelect(fr *frame, instr *ssa.Select) nextInstr {
	var cases []reflect.SelectCase
	if !instr.Blocking {
		cases = append(cases, reflect.SelectCase{
			Dir: reflect.SelectDefault,
		})
	}
	for _, state := range instr.States {
		var dir reflect.SelectDir
		if state.Dir == types.RecvOnly {
			dir = reflect.SelectRecv
		} else {
			dir = reflect.SelectSend
		}
		var send reflect.Value
		if state.Send != nil {
			send = reflect.ValueOf(fr.get(state.Send))
		}
		cases = append(cases, reflect.SelectCase{
			Dir:  dir,
			Chan: reflect.ValueOf(fr.get(state.Chan)),
			Send: send,
		})
	}
	chosen, recv, recvOk := reflect.Select(cases)
	if !instr.Blocking {
		chosen-- // default case should have index -1.
	}
	r := []internal.Value{internal.ValueOf(chosen), internal.ValueOf(recvOk)}
	for i, st := range instr.States {
		if st.Dir == types.RecvOnly {
			var v internal.Value
			if i == chosen && recvOk {
				v = internal.RValue{Value: recv}
			} else {
				v = zero(st.Chan.Type().Underlying().(*types.Chan).Elem())
			}
			r = append(r, v)
		}
	}
	fr.set(instr, internal.ValueOf(r))
	return _NEXT
}
