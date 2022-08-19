package goscript

import (
	"fmt"
	"go/types"

	"github.com/goccy/go-reflect"

	"github.com/linkxzhou/http_bench/goscript/internal"
	"golang.org/x/tools/go/ssa"
)

func deref(typ types.Type) types.Type {
	if p, ok := typ.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return typ
}

func zero(t types.Type) internal.Value {
	v := reflect.New(typeCopy(t))
	return internal.RValue{Value: v}
}

func ssaStack(state *State) {
	var instr ssa.Instruction
	defer func() {
		if state.block == nil {
			return // normal return
		}
		state.panicking = true
		state.panic = fmt.Errorf("panic: %s: %v", state.program.MainPkg.Prog.Fset.Position(instr.Pos()).String(), recover())
		state.callDefers()
		state.block = state.fn.Recover
	}()
	for {
		for _, instr = range state.block.Instrs {
			c := visitInstr(state, instr)
			if !state.debugging {
				if err := state.context.Err(); err != nil {
					panic(err)
				}
			}
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

type nextInstr int

const (
	_NEXT   nextInstr = iota // next
	_Return                  // return
	_JUMP                    // jump to block
)

func visitInstr(state *State, instr ssa.Instruction) nextInstr {
	c := _NEXT
	switch instr := instr.(type) {
	case *ssa.DebugRef:
		// pass
	case *ssa.Alloc:
		c = ssaAlloc(state, instr)
	case *ssa.UnOp:
		c = ssaUnOp(state, instr)
	case *ssa.BinOp:
		c = ssaBinOp(state, instr)
	case *ssa.MakeInterface:
		c = ssaMakeInterface(state, instr)
	case *ssa.Return:
		c = ssaReturn(state, instr)
	case *ssa.IndexAddr:
		c = ssaIndexAddr(state, instr)
	case *ssa.Field:
		c = ssaField(state, instr)
	case *ssa.FieldAddr:
		c = ssaFieldAddr(state, instr)
	case *ssa.Store:
		c = ssaStore(state, instr)
	case *ssa.Slice:
		c = ssaSlice(state, instr)
	case *ssa.Call:
		c = ssaCall(state, instr)
	case *ssa.MakeSlice:
		c = ssaMakeSlice(state, instr)
	case *ssa.MakeMap:
		c = ssaMakeMap(state, instr)
	case *ssa.MapUpdate:
		c = ssaMapUpdate(state, instr)
	case *ssa.Lookup:
		c = ssaLookup(state, instr)
	case *ssa.Extract:
		c = ssaExtract(state, instr)
	case *ssa.If:
		c = ssaIf(state, instr)
	case *ssa.Jump:
		c = ssaJump(state, instr)
	case *ssa.Phi:
		c = ssaPhi(state, instr)
	case *ssa.Convert:
		c = ssaConvert(state, instr)
	case *ssa.Range:
		c = ssaRange(state, instr)
	case *ssa.Next:
		c = ssaNext(state, instr)
	case *ssa.ChangeType:
		c = ssaChangeType(state, instr)
	case *ssa.ChangeInterface:
		c = ssaChangeInterface(state, instr)
	case *ssa.MakeClosure:
		c = ssaMakeClosure(state, instr)
	case *ssa.Defer:
		c = ssaDefer(state, instr)
	case *ssa.RunDefers:
		c = ssaRunDefers(state, instr)
	case *ssa.MakeChan:
		c = ssaMakeChan(state, instr)
	case *ssa.Send:
		c = ssaSend(state, instr)
	case *ssa.TypeAssert:
		c = ssaTypeAssert(state, instr)
	case *ssa.Go:
		c = ssaGo(state, instr)
	case *ssa.Panic:
		c = ssaPanic(state, instr)
	case *ssa.Select:
		c = ssaSelect(state, instr)
	default:
		panic(fmt.Sprintf("unexpected instruction: %T", instr))
	}
	return c
}

func ssaAlloc(state *State, instr *ssa.Alloc) nextInstr {
	var addr internal.Value
	if instr.Heap {
		addr = *new(internal.Value)
		state.env[instr] = addr
	} else {
		addr = state.env[instr]
	}
	addr = zero(deref(instr.Type()))
	return _NEXT
}

func ssaUnOp(state *State, instr *ssa.UnOp) nextInstr {
	v := unop(instr, state.get(instr.X))
	state.set(instr, v)
	return _NEXT
}

func ssaBinOp(state *State, instr *ssa.BinOp) nextInstr {
	v := binop(instr, state.get(instr.X), state.get(instr.Y))
	state.set(instr, v)
	return _NEXT
}

func ssaMakeInterface(state *State, instr *ssa.MakeInterface) nextInstr {
	v := state.get(instr.X)
	state.set(instr, v)
	return _NEXT
}

func ssaReturn(state *State, instr *ssa.Return) nextInstr {
	switch len(instr.Results) {
	case 0:
	case 1:
		state.result = state.get(instr.Results[0])
	default:
		// 返回多值时，对返回值进行打包
		var res []internal.Value
		for _, r := range instr.Results {
			res = append(res, state.get(r))
		}
		state.result = internal.ValueOf(res)
	}
	state.block = nil
	return _Return
}

func ssaIndexAddr(state *State, instr *ssa.IndexAddr) nextInstr {
	x := state.get(instr.X)
	idx := int(state.get(instr.Index).Int())
	state.set(instr, internal.RValue{Value: x.Elem().RValue().Index(idx).Addr()})
	return _NEXT
}

func ssaField(state *State, instr *ssa.Field) nextInstr {
	x := state.get(instr.X)
	state.set(instr, x.Field(instr.Field))
	return _NEXT
}

func ssaFieldAddr(state *State, instr *ssa.FieldAddr) nextInstr {
	x := state.get(instr.X).Elem()
	v := x.RValue().Field(instr.Field).Addr()
	state.set(instr, internal.RValue{Value: v})
	return _NEXT
}

func ssaStore(state *State, instr *ssa.Store) nextInstr {
	switch addr := instr.Addr.(type) {
	case *ssa.Alloc, *ssa.FreeVar:
		v := state.get(instr.Val)
		state.env[addr].Elem().Set(v)
	case *ssa.Global:
		v := state.get(instr.Val)
		state.program.globals[addr] = &v
	case *internal.ExternalValue:
		v := state.get(instr.Val)
		addr.Store(v)
	case *ssa.IndexAddr:
		index := int(state.get(addr.Index).Int())
		x := state.get(addr.X).Elem()
		val := state.get(instr.Val)
		x.Index(index).Set(val)
	default:
		v := state.env[addr]
		v.Elem().Set(state.get(instr.Val))
	}
	return _NEXT
}

func ssaSlice(state *State, instr *ssa.Slice) nextInstr {
	x := state.get(instr.X).Elem()
	l, h := 0, x.Len()
	low := state.get(instr.Low)
	if low != nil {
		l = int(low.Int())
	}
	high := state.get(instr.High)
	if high != nil {
		h = int(high.Int())
	}
	max := state.get(instr.Max)
	if max != nil {
		state.set(instr, internal.RValue{Value: x.RValue().Slice3(l, h, int(max.Int()))})
	} else {
		state.set(instr, internal.RValue{Value: x.RValue().Slice(l, h)})
	}
	return _NEXT
}

func ssaCall(state *State, instr *ssa.Call) nextInstr {
	if v := callOp(state, instr.Common()); v != nil {
		state.env[instr] = v
	}
	return _NEXT
}

func ssaMakeSlice(state *State, instr *ssa.MakeSlice) nextInstr {
	sliceLen := int(state.get(instr.Len).Int())
	sliceCap := int(state.get(instr.Cap).Int())
	state.set(instr, internal.RValue{Value: reflect.MakeSlice(typeCopy(instr.Type()), sliceLen, sliceCap)})
	return _NEXT
}

func ssaMakeMap(state *State, instr *ssa.MakeMap) nextInstr {
	state.set(instr, internal.RValue{Value: reflect.MakeMap(typeCopy(instr.Type()))})
	return _NEXT
}

func ssaMapUpdate(state *State, instr *ssa.MapUpdate) nextInstr {
	m := state.get(instr.Map)
	key := state.get(instr.Key)
	v := state.get(instr.Value)
	m.Elem().RValue().SetMapIndex(key.RValue(), v.RValue())
	return _NEXT
}

func ssaLookup(state *State, instr *ssa.Lookup) nextInstr {
	x := state.get(instr.X)
	index := state.get(instr.Index)
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
		state.set(instr, v)
	} else {
		state.set(instr, x.Index(int(index.Int())))
	}
	return _NEXT
}

func ssaExtract(state *State, instr *ssa.Extract) nextInstr {
	state.set(instr, state.get(instr.Tuple).Index(instr.Index).Interface().(internal.Value))
	return _NEXT
}

func ssaIf(state *State, instr *ssa.If) nextInstr {
	succ := 1
	if state.get(instr.Cond).Bool() {
		succ = 0
	}
	state.prevBlock, state.block = state.block, state.block.Succs[succ]
	return _JUMP
}

func ssaJump(state *State, instr *ssa.Jump) nextInstr {
	state.prevBlock, state.block = state.block, state.block.Succs[0]
	return _JUMP
}

func ssaPhi(state *State, instr *ssa.Phi) nextInstr {
	for i, pred := range instr.Block().Preds {
		if state.prevBlock == pred {
			state.set(instr, state.get(instr.Edges[i]))
			break
		}
	}
	return _NEXT
}

func ssaConvert(state *State, instr *ssa.Convert) nextInstr {
	state.set(instr, conv(state.get(instr.X).Interface(), instr.Type()))
	return _NEXT
}

func ssaRange(state *State, instr *ssa.Range) nextInstr {
	v := state.get(instr.X)
	state.set(instr, &internal.MapIter{
		I:     0,
		Value: v,
		Keys:  v.RValue().MapKeys(),
	})
	return _NEXT
}

func ssaNext(state *State, instr *ssa.Next) nextInstr {
	state.set(instr, state.get(instr.Iter).Next())
	return _NEXT
}

func ssaChangeType(state *State, instr *ssa.ChangeType) nextInstr {
	state.set(instr, state.get(instr.X))
	return _NEXT
}

func ssaChangeInterface(state *State, instr *ssa.ChangeInterface) nextInstr {
	state.set(instr, state.get(instr.X))
	return _NEXT
}

func ssaMakeClosure(state *State, instr *ssa.MakeClosure) nextInstr {
	closure := state.makeFunc(instr.Fn.(*ssa.Function), instr.Bindings)
	state.set(instr, closure)
	return _NEXT
}

func ssaDefer(state *State, instr *ssa.Defer) nextInstr {
	state.defers = append(state.defers, instr)
	return _NEXT
}

func ssaRunDefers(state *State, instr *ssa.RunDefers) nextInstr {
	state.callDefers()
	return _NEXT
}

func ssaMakeChan(state *State, instr *ssa.MakeChan) nextInstr {
	state.set(instr, internal.RValue{Value: reflect.MakeChan(typeCopy(instr.Type()), int(state.get(instr.Size).Int()))})
	return _NEXT
}

func ssaSend(state *State, instr *ssa.Send) nextInstr {
	state.get(instr.Chan).RValue().Send(state.get(instr.X).RValue())
	return _NEXT
}

func ssaTypeAssert(state *State, instr *ssa.TypeAssert) nextInstr {
	v := state.get(instr.X)
	for v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	destType := typeCopy(instr.AssertedType)

	var assignable bool
	if v.Kind() == reflect.Invalid {
		assignable = false
	} else {
		assignable = v.Type().AssignableTo(destType)
	}

	switch {
	case instr.CommaOk && assignable:
		state.set(instr, internal.ValueOf([]internal.Value{v, internal.ValueOf(true)}))

	case instr.CommaOk && !assignable:
		state.set(instr, internal.ValueOf([]internal.Value{internal.RValue{Value: reflect.Zero(destType)}, internal.ValueOf(false)}))

	case !instr.CommaOk && assignable:
		state.set(instr, v)

	case !instr.CommaOk && !assignable:
		if v.Kind() == reflect.Invalid {
			panic(fmt.Errorf("interface conversion: interface is nil, not %s", destType.String()))
		} else {
			panic(fmt.Errorf("interface conversion: interface is %s, not %s", v.Type().String(), destType.String()))
		}
	}
	return _NEXT
}

func ssaGo(state *State, instr *ssa.Go) nextInstr {
	goCall(state, instr.Common())
	return _NEXT
}

func ssaPanic(state *State, instr *ssa.Panic) nextInstr {
	panic(state.get(instr.X).Interface())
}

func ssaSelect(state *State, instr *ssa.Select) nextInstr {
	var cases []reflect.SelectCase
	if !instr.Blocking {
		cases = append(cases, reflect.SelectCase{
			Dir: reflect.SelectDefault,
		})
	}
	for _, v := range instr.States {
		var dir reflect.SelectDir
		if v.Dir == types.RecvOnly {
			dir = reflect.SelectRecv
		} else {
			dir = reflect.SelectSend
		}
		var send reflect.Value
		if v.Send != nil {
			send = reflect.ValueOf(state.get(v.Send))
		}
		cases = append(cases, reflect.SelectCase{
			Dir:  dir,
			Chan: reflect.ValueOf(state.get(v.Chan)),
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
	state.set(instr, internal.ValueOf(r))
	return _NEXT
}
