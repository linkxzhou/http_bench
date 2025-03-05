package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"io"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
)

const (
	gopath   = "./http_bench"
	duration = 10
	testTimeout = 30 * time.Second // 测试超时时间
)

type command struct {
	cmder *exec.Cmd
}

func withPrintln(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}

func (c *command) init(cmd string, args []string) {
	fmt.Println("cmd args: ", strings.Join(args, " "))
	c.cmder = exec.Command(cmd, args...)
	c.cmder.Env = os.Environ()
	c.cmder.Dir, _ = os.Getwd()
}

func (c *command) startup() (string, error) {
	if c.cmder == nil {
		return "", errors.New("invalid command")
	}

	output, err := c.cmder.CombinedOutput()
	return string(output), err
}

func (c *command) stop() error {
	if c.cmder == nil {
		return errors.New("invalid command")
	}

	return c.cmder.Process.Kill()
}

// setupServer 创建并启动一个通用服务器
func setupServer(name, listen string, serverType string) (interface{}, *sync.WaitGroup) {
	var wg sync.WaitGroup
	mux := http.NewServeMux()
	
	switch serverType {
	case "ws":
		var upgrader = websocket.Upgrader{}
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
	default: // http1, http2, http3
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer r.Body.Close()
			if len(body) == 0 {
				w.Write([]byte(fmt.Sprintf("this is empty body, type: %s", name)))
			}
			w.Write(body)
		})
	}

	switch serverType {
	case "http3":
		srv := &http3.Server{Addr: listen, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.ListenAndServeTLS("./test/server.crt", "./test/server.key"); err != nil {
				fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
			}
			fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
		}()
		return srv, &wg
	case "ws":
		fallthrough
	default:
		srv := &http.Server{Addr: listen, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if serverType == "http2" {
				err = srv.ListenAndServeTLS("./test/server.crt", "./test/server.key")
			} else {
				err = srv.ListenAndServe()
			}
			if err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
			}
			fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
		}()
		return srv, &wg
	}
}

// runCommand 执行命令并检查结果
func runCommand(t *testing.T, name, args string, expectError bool) string {
	cmder := command{}
	cmder.init(gopath, strings.Split(args, " "))
	result, err := cmder.startup()
	if err != nil || (strings.Contains(result, "err") || 
		strings.Contains(result, "error") || 
		strings.Contains(result, "ERROR")) {
		if !expectError {
			t.Errorf("startup error: %v, result: %v", err, result)
		}
	}
	fmt.Println(name+" | result: ", result)
	return result
}

func TestStressHTTP1(t *testing.T) {
	name := "http1"
	listen := "127.0.0.1:18091"
	srv, wg := setupServer(name, listen, "http1")
	http1Srv := srv.(*http.Server)

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url http://%s/`, duration, name, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url-file %s`, duration, name, `./test/urls.txt`),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' http://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s http://%s/`, duration, name, `./test/body.txt`, listen),
			isErr: false,
		},
	} {
		runCommand(t, name, v.args, v.isErr)
	}

	http1Srv.Close()
	wg.Wait()
}

// openssl req -newkey rsa:2048 -nodes -keyout server.key -x509 -days 365 -out server.crt
func TestStressHTTP2(t *testing.T) {
	name := "http2"
	listen := "127.0.0.1:18091"
	srv, wg := setupServer(name, listen, "http2")
	http2Srv := srv.(*http.Server)

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`, duration, name, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' http://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s http://%s/`, duration, name, `./test/body.txt`, listen),
			isErr: false,
		},
	} {
		runCommand(t, name, v.args, v.isErr)
	}

	http2Srv.Close()
	wg.Wait()
}

func TestStressHTTP3(t *testing.T) {
	name := "http3"
	listen := "127.0.0.1:18091"
	srv, wg := setupServer(name, listen, "http3")
	http3Srv := srv.(*http3.Server)

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`, duration, name, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' http://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s http://%s/`, duration, name, `./test/body.txt`, listen),
			isErr: false,
		},
	} {
		runCommand(t, name, v.args, v.isErr)
	}

	http3Srv.Close()
	wg.Wait()
}

func TestStressWS(t *testing.T) {
	name := "ws"
	listen := "127.0.0.1:18091"
	srv, wg := setupServer(name, listen, "ws")
	wsSrv := srv.(*http.Server)

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -url ws://%s/`, duration, name, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' ws://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s ws://%s/`, duration, name, `./test/body.txt`, listen),
			isErr: false,
		},
	} {
		runCommand(t, name, v.args, v.isErr)
	}

	wsSrv.Close()
	wg.Wait()
}

// TODO: github ci has error and run local.
func TestStressMultipleWorkerHTTP1(t *testing.T) {
	name := "http1"
	listen := "127.0.0.1:18091"
	srv, wg := setupServer(name, listen, "http1")
	http1Srv := srv.(*http.Server)

	workerList := []string{"127.0.0.1:12710", "127.0.0.1:12711"}
	for _, v := range []struct {
		args    string
		workers []string
		isErr   bool
	}{
		{
			args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body "%s" -url http://%s/ -W %s -W %s`,
				duration, name, `{}`, listen, workerList[0], workerList[1]),
			workers: []string{
				fmt.Sprintf(`-listen %s`, workerList[0]),
				fmt.Sprintf(`-listen %s`, workerList[1]),
			},
			isErr: false,
		},
	} {
		var cmderList = []command{}
		var cmderCg sync.WaitGroup

		for _, worker := range v.workers {
			cmderCg.Add(1)
			workerCmd := command{}
			workerCmd.init(gopath, strings.Split(worker, " "))
			go func() {
				workerResult, _ := workerCmd.startup()
				cmderCg.Done()
				fmt.Println("workerResult: ", workerResult)
			}()
			cmderList = append(cmderList, workerCmd)
		}

		time.Sleep(10 * time.Second) // wait for 10s

		runCommand(t, name, v.args, v.isErr)

		// stop all workers
		for _, workerCmd := range cmderList {
			workerCmd.stop()
		}
		cmderCg.Wait()
	}

	http1Srv.Close()
	wg.Wait()
}

var tcpHandleStop bool

func tcpHandleConnection(conn net.Conn) error {
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer[0:1024])
		if err != nil {
			return err
		}

		if tcpHandleStop {
			return nil
		}

		message := string(buffer[:n])
		response := fmt.Sprintf("recv: %s", message)
		_, err = conn.Write([]byte(response))
		if err != nil {
			return err
		}
	}
}

func TestStressTCP(t *testing.T) {
	name := "tcp"
	body := "this is stress body"
	bodyHex := "746869732069732073747265737320626f6479"
	listen := "127.0.0.1:18091"
	srv, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Println(name+" | srv err: ", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			conn, err := srv.Accept()
			fmt.Println("conn: ", conn, "err: ", err)
			if err != nil {
				break
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				err = tcpHandleConnection(c) // 修改这里，使用传入的参数c而不是外部的conn
				fmt.Println("tcpHandleConnection err: ", err)
			}(conn)
		}
	}()

	for _, v := range []struct {
		args  string
		isErr bool
	}{
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -p %s -body "%s" -url %s`, duration, name, body, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -p %s -bodytype hex -body %s -url %s`, duration, name, bodyHex, listen),
			isErr: false,
		},
	} {
		runCommand(t, name, v.args, v.isErr)
	}

	tcpHandleStop = true // stop server
	srv.Close()
	wg.Wait()
}
