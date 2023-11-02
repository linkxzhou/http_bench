package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
)

const (
	gopath   = "./http_bench"
	duration = 10
)

func startupHttpBench(cmd string, args []string) (string, error) {
	cmder := exec.Command(cmd, args...)
	cmder.Env = os.Environ()
	cmder.Dir, _ = os.Getwd()
	output, err := cmder.CombinedOutput()
	return string(output), err
}

func TestStressHTTP1(t *testing.T) {
	name := "http1"
	listen := "127.0.0.1:18091"

	var wg sync.WaitGroup
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`This is ` + name + ` Echo Server`))
	})
	srv := &http.Server{Addr: listen, Handler: mux}

	go func() {
		wg.Add(1)
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
		}
		fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
	}()

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url http://%s/`, duration, name, listen),
			isErr: false,
		},
	} {
		result, err := startupHttpBench(gopath, strings.Split(v.args, " "))
		if err != nil || (strings.Contains(result, "err") || strings.Contains(result, "error") || strings.Contains(result, "ERROR")) {
			if !v.isErr {
				t.Errorf("startupHttpBench error: %v, result: %v", err, result)
			}
		}
		fmt.Println(name+" | result: ", result)
	}

	srv.Shutdown(context.TODO())
	wg.Wait()
}

// openssl req -newkey rsa:2048 -nodes -keyout server.key -x509 -days 365 -out server.crt
func TestStressHTTP2(t *testing.T) {
	name := "http2"
	listen := "127.0.0.1:18091"

	var wg sync.WaitGroup
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`This is ` + name + ` Echo Server`))
	})
	srv := &http.Server{Addr: listen, Handler: mux}

	go func() {
		wg.Add(1)
		defer wg.Done()
		if err := srv.ListenAndServeTLS("./test/server.crt", "./test/server.key"); err != nil {
			fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
		}
		fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
	}()

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`, duration, name, listen),
			isErr: false,
		},
	} {
		result, err := startupHttpBench(gopath, strings.Split(v.args, " "))
		if err != nil || (strings.Contains(result, "err") || strings.Contains(result, "error") || strings.Contains(result, "ERROR")) {
			if !v.isErr {
				t.Errorf("startupHttpBench error: %v, result: %v", err, result)
			}
		}
		fmt.Println(name+" | result: ", result)
	}

	srv.Shutdown(context.TODO())
	wg.Wait()
}

func TestStressHTTP3(t *testing.T) {
	name := "http3"
	listen := "127.0.0.1:18091"

	var wg sync.WaitGroup
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`This is ` + name + ` Echo Server`))
	})
	srv := &http3.Server{Addr: listen, Handler: mux}

	go func() {
		wg.Add(1)
		defer wg.Done()
		if err := srv.ListenAndServeTLS("./test/server.crt", "./test/server.key"); err != nil {
			fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
		}
		fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
	}()

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`, duration, name, listen),
			isErr: false,
		},
	} {
		result, err := startupHttpBench(gopath, strings.Split(v.args, " "))
		if err != nil || (strings.Contains(result, "err") || strings.Contains(result, "error") || strings.Contains(result, "ERROR")) {
			if !v.isErr {
				t.Errorf("startupHttpBench error: %v, result: %v", err, result)
			}
		}
		fmt.Println(name+" | result: ", result)
	}

	srv.Close()
	wg.Wait()
}

func TestStressWS(t *testing.T) {
	name := "ws"
	listen := "127.0.0.1:18091"
	var upgrader = websocket.Upgrader{} // use default options

	var wg sync.WaitGroup
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				break
			}
			err = c.WriteMessage(mt, message)
			if err != nil {
				break
			}
		}
	})
	srv := &http.Server{Addr: listen, Handler: mux}

	go func() {
		wg.Add(1)
		defer wg.Done()
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
		}
		fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
	}()

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url ws://%s/`, duration, name, listen),
			isErr: false,
		},
	} {
		result, err := startupHttpBench(gopath, strings.Split(v.args, " "))
		if err != nil || (strings.Contains(result, "err") || strings.Contains(result, "error") || strings.Contains(result, "ERROR")) {
			if !v.isErr {
				t.Errorf("startupHttpBench error: %v, result: %v", err, result)
			}
		}
		fmt.Println(name+" | result: ", result)
	}

	srv.Close()
	wg.Wait()
}
