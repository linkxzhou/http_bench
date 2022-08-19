package goscript

import (
	"fmt"
	"os"
	"runtime/pprof"
	"testing"

	"github.com/goccy/go-reflect"

	lua2 "github.com/Shopify/go-lua"
	"github.com/linkxzhou/http_bench/goscript"
	lua "github.com/yuin/gopher-lua"
)

func TestImport(t *testing.T) {
	sources := `
	package test

	import "pkg1"
	import "pkg2"

	var A = "1"
	func test() string {
		return A + pkg1.F() + pkg2.S
	}
	`

	pkg1 := `
package pkg1
func F() string {
	return "hello"
}
`

	pkg2 := `
package pkg2
const S = "world"
func F() string {
	return "world"
}
`
	p1, err := goscript.BuildProgram("pkg1", pkg1)
	if err != nil {
		t.Error(err)
		return
	}
	p2, err := goscript.BuildProgram("pkg2", pkg2)
	if err != nil {
		t.Error(err)
		return
	}
	p, err := goscript.BuildProgram("main", sources, p1.MainPkg, p2.MainPkg)
	if err != nil {
		t.Error(err)
		return
	}
	out, err := p.Run("test")
	if err != nil {
		t.Error(err)
		return
	}
	expected := "1helloworld"
	if !reflect.DeepEqual(out, expected) {
		t.Errorf("Expected %#v got %#v.", expected, out)
	}
}

// func BenchmarkFib(b *testing.B) {
// 	b.StopTimer()
// 	b.ReportAllocs()
// 	code := `
// package test

// func fib(i int) int {
// 	if i < 2 {
// 		return i
// 	}
// 	return fib(i - 1) + fib(i - 2)
// }

// func test(i int) int {
// 	return fib(i)
// }
// `
// 	interpreter, err := goscript.BuildProgram("test", code)
// 	if err != nil {
// 		b.Error(err)
// 		return
// 	}

// 	var ret interface{}
// 	f, err := os.Create("prof.out")
// 	if err != nil {
// 		b.Error(err)
// 	}
// 	_ = pprof.StartCPUProfile(f)

// 	b.StartTimer()
// 	for i := 0; i < b.N; i++ {
// 		ret, err = interpreter.Run("test", 25)
// 	}
// 	b.Log(ret, err)
// 	pprof.StopCPUProfile()
// }

const fibCycle = 32
const fibN = 1

func TestFib0(t *testing.T) {
	sources := `
package test

func fib(i int) int {
	if i < 2 {
		return i
	}
	return fib(i - 1) + fib(i - 2)
}

func test(i int) int {
	return fib(i)
}
	`
	interpreter, err := goscript.BuildProgram("test", sources)
	if err != nil {
		return
	}
	for i := 0; i < fibN; i++ {
		r, _ := interpreter.Run("test", 10)
		fmt.Println("r: ", r)
	}
}

func TestFib1(t *testing.T) {
	f, err := os.Create("pprof.out")
	sources := `
package test

func fib(i int) int {
	if i < 2 {
		return i
	}
	return fib(i - 1) + fib(i - 2)
}

func test(i int) int {
	return fib(i)
}
	`
	interpreter, err := goscript.BuildProgram("test", sources)
	if err != nil {
		return
	}
	for i := 0; i < fibN; i++ {
		_ = pprof.StartCPUProfile(f)
		r, _ := interpreter.Run("test", fibCycle)
		pprof.StopCPUProfile()
		fmt.Println("r: ", r)
	}
}

func fib(i int) int {
	if i < 2 {
		return i
	}
	return fib(i-1) + fib(i-2)
}

func TestFib2(t *testing.T) {
	for i := 0; i < fibN; i++ {
		r := fib(fibCycle)
		fmt.Println("r: ", r)
	}
}

func TestFibLua1(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	for i := 0; i < fibN; i++ {
		if err := L.DoString(`local function Fibonacci_1(n)
		if n == 1 or n == 2 then
			return 1
		else
			return Fibonacci_1(n - 1) + Fibonacci_1(n - 2)
		end
	end
	print(Fibonacci_1(` + fmt.Sprintf("%d", fibCycle) + `))`); err != nil {
			panic(err)
		}
	}
}

func TestFibLua2(t *testing.T) {
	L := lua2.NewState()
	lua2.OpenLibraries(L)
	for i := 0; i < fibN; i++ {
		if err := lua2.DoString(L, `local function Fibonacci_1(n)
		if n == 1 or n == 2 then
			return 1
		else
			return Fibonacci_1(n - 1) + Fibonacci_1(n - 2)
		end
	end
	print(Fibonacci_1(`+fmt.Sprintf("%d", fibCycle)+`))`); err != nil {
			panic(err)
		}
	}
}
