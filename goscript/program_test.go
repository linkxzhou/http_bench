package goscript

import (
	"fmt"
	"os"
	"runtime/pprof"
	"testing"

	"github.com/linkxzhou/http_bench/goscript"
)

// // TestImportGofun 测试将gofun编译成的库导入到其他gofun程序中
// func TestImportGofun(t *testing.T) {
// 	sources := `
// 	package test

// 	import "pkg1"
// 	import "pkg2"

// 	var A = "1"
// 	func test() string {
// 		return A + pkg1.F() + pkg2.S
// 	}
// 	`

// 	pkg1 := `
// package pkg1
// func F() string {
// 	return "hello"
// }
// `

// 	pkg2 := `
// package pkg2
// const S = "world"
// func F() string {
// 	return "world"
// }
// `
// 	p1, err := BuildProgram("pkg1", pkg1)
// 	if err != nil {
// 		t.Error(err)
// 		return
// 	}
// 	p2, err := BuildProgram("pkg2", pkg2)
// 	if err != nil {
// 		t.Error(err)
// 		return
// 	}

// 	p, err := BuildProgram("main", sources, p1.mainPkg, p2.mainPkg)
// 	if err != nil {
// 		t.Error(err)
// 		return
// 	}

// 	out, err := p.Run("test")
// 	if err != nil {
// 		t.Error(err)
// 		return
// 	}
// 	expected := "1helloworld"
// 	if !reflect.DeepEqual(out, expected) {
// 		t.Errorf("Expected %#v got %#v.", expected, out)
// 	}
// }

// BenchmarkFib 递归计算斐波那契数列，测试gofun的执行性能
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

const fibCycle = 5
const fibN = 1

// TestFib1 递归计算斐波那契数列，测试gofun的执行性能
func TestFib1(t *testing.T) {
	f, err := os.Create("pprof.out")
	_ = pprof.StartCPUProfile(f)
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
		r, _ := interpreter.Run("test", fibCycle)
		fmt.Println("r: ", r)
	}
	pprof.StopCPUProfile()
}

func fib(i int) int {
	if i < 2 {
		return i
	}
	return fib(i-1) + fib(i-2)
}

// TestFib 递归计算斐波那契数列，测试gofun的执行性能
func TestFib2(t *testing.T) {
	for i := 0; i < fibN; i++ {
		r := fib(fibCycle)
		fmt.Println("r: ", r)
	}
}

// // TestTimeout 测试函数超时后能否强制终止执行
// func TestTimeout(t *testing.T) {
// 	sources := `
// package main

// import "time"

// func test() string {
// 	for {
// 		go func() {
// 			for {
// 				time.Sleep(1 * time.Second)
// 			}
// 		}()
// 	    time.Sleep(2 * time.Second)
// 	}
// 	return "unreachable"
// }
// 	`
// 	_, err := goscript.Run(sources, "test")
// 	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
// 		t.Errorf("Expected timeout got %#v.", err)
// 	}
// }
