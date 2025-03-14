package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
)

const (
	gopath      = "./http_bench"
	duration    = 10
	testTimeout = 30 * time.Second // Test timeout duration
)

type command struct {
	cmder *exec.Cmd
	ctx   context.Context
	cancel context.CancelFunc
}

func withPrintln(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
}

func (c *command) init(cmd string, args []string) {
	fmt.Println("cmd args: ", strings.Join(args, " "))
	c.ctx, c.cancel = context.WithTimeout(context.Background(), testTimeout)
	c.cmder = exec.CommandContext(c.ctx, cmd, args...)
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
	
	if c.cancel != nil {
		c.cancel()
	}
	
	return c.cmder.Process.Kill()
}

// setupServer creates and starts a generic server
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
				return
			}
			w.Write(body)
		})
	}

	// Create server context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout*2)
	
	switch serverType {
	case "http3":
		srv := &http3.Server{
			Addr:    listen, 
			Handler: mux,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			
			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.ListenAndServeTLS("./test/server.crt", "./test/server.key")
			}()
			
			select {
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
				}
			case <-ctx.Done():
				// Context timeout or cancellation
			}
			
			fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
		}()
		return srv, &wg
		
	case "ws":
		fallthrough
	default:
		srv := &http.Server{
			Addr:         listen, 
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			
			var err error
			errCh := make(chan error, 1)
			
			go func() {
				if serverType == "http2" {
					errCh <- srv.ListenAndServeTLS("./test/server.crt", "./test/server.key")
				} else {
					errCh <- srv.ListenAndServe()
				}
			}()
			
			select {
			case err = <-errCh:
				if err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
				}
			case <-ctx.Done():
				// Context timeout or cancellation
				srv.Shutdown(context.Background())
			}
			
			fmt.Fprintf(os.Stdout, name+" Server listen %s\n", listen)
		}()
		return srv, &wg
	}
}

// runCommand executes a command and checks the result
func runCommand(t *testing.T, name, args string, expectError bool) string {
	cmder := command{}
	cmder.init(gopath, strings.Split(args, " "))
	
	result, err := cmder.startup()
	
	hasError := err != nil || strings.Contains(result, "err") || 
		strings.Contains(result, "error") || 
		strings.Contains(result, "ERROR")
		
	if hasError != expectError {
		t.Errorf("startup error mismatch: got error=%v, expected error=%v, result: %v", 
			hasError, expectError, result)
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
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' https://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s https://%s/`, duration, name, `./test/body.txt`, listen),
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
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' https://%s/`, duration, name, `{"key":"value"}`, listen),
			isErr: false,
		},
		{
			args:  fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s https://%s/`, duration, name, `./test/body.txt`, listen),
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
		var workerReady sync.WaitGroup
		
		// Start worker processes
		for _, worker := range v.workers {
			cmderCg.Add(1)
			workerReady.Add(1)
			workerCmd := command{}
			workerCmd.init(gopath, strings.Split(worker, " "))
			
			go func() {
				defer cmderCg.Done()
				
				// Signal that worker is starting
				workerReady.Done()
				
				workerResult, _ := workerCmd.startup()
				fmt.Println("workerResult: ", workerResult)
			}()
			
			cmderList = append(cmderList, workerCmd)
		}
		
		// Wait for workers to initialize
		workerReady.Wait()
		time.Sleep(5 * time.Second)
		
		// Run the main command
		runCommand(t, name, v.args, v.isErr)

		// Stop all workers
		for i := range cmderList {
			cmderList[i].stop()
		}
		
		// Wait for all workers to terminate
		cmderCg.Wait()
	}

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	http1Srv.Shutdown(ctx)
	wg.Wait()
}

// tcpHandleConnection handles TCP connections for the TCP test server
func tcpHandleConnection(conn net.Conn, stopCh <-chan struct{}) error {
	buffer := make([]byte, 1024)
	
	// Set read deadline to prevent blocking forever
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	for {
		select {
		case <-stopCh:
			return nil
		default:
			n, err := conn.Read(buffer[0:1024])
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Reset deadline and continue
					conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					continue
				}
				return err
			}

			message := string(buffer[:n])
			response := fmt.Sprintf("recv: %s", message)
			
			// Set write deadline
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write([]byte(response))
			if err != nil {
				return err
			}
			
			// Reset read deadline
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		}
	}
}

// TestStressTCP tests TCP stress testing，TODO：github ci has error and run local.
// func TestStressTCP(t *testing.T) {
// 	name := "tcp"
// 	body := "this is stress body"
// 	bodyHex := "746869732069732073747265737320626f6479"
// 	listen := "127.0.0.1:18091"
	
// 	// Create TCP listener
// 	srv, err := net.Listen("tcp", listen)
// 	if err != nil {
// 		t.Fatalf("%s | srv err: %v", name, err)
// 		return
// 	}
	
// 	stopCh := make(chan struct{})
// 	var wg sync.WaitGroup
// 	wg.Add(1)
	
// 	// Start TCP server
// 	go func() {
// 		defer wg.Done()
// 		defer close(stopCh)

// 		for {
// 			// Set accept deadline
// 			srv.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			
// 			conn, err := srv.Accept()
// 			if err != nil {
// 				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
// 					// Check if we should stop
// 					select {
// 					case <-stopCh:
// 						return
// 					default:
// 						continue
// 					}
// 				}
// 				fmt.Println("Accept error:", err)
// 				return
// 			}

// 			wg.Add(1)
// 			go func(c net.Conn) {
// 				defer wg.Done()
// 				defer c.Close()
				
// 				err := tcpHandleConnection(c, stopCh)
// 				if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
// 					fmt.Println("tcpHandleConnection err:", err)
// 				}
// 			}(conn)
// 		}
// 	}()

// 	// Run tests
// 	for _, v := range []struct {
// 		args  string
// 		isErr bool
// 	}{
// 		{
// 			args:  fmt.Sprintf(`-c 1 -d %ds -p %s -body "%s" -url %s`, duration, name, body, listen),
// 			isErr: false,
// 		},
// 		{
// 			args:  fmt.Sprintf(`-c 1 -d %ds -p %s -bodytype hex -body %s -url %s`, duration, name, bodyHex, listen),
// 			isErr: false,
// 		},
// 	} {
// 		runCommand(t, name, v.args, v.isErr)
// 	}

// 	// Stop server
// 	close(stopCh)
// 	srv.Close()
	
// 	// Wait with timeout
// 	done := make(chan struct{})
// 	go func() {
// 		wg.Wait()
// 		close(done)
// 	}()
	
// 	select {
// 	case <-done:
// 		// All goroutines finished
// 	case <-time.After(10 * time.Second):
// 		t.Log("Warning: TCP test timed out waiting for goroutines")
// 	}
// }
