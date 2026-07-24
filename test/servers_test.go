package test

// Integration test helper servers for long-running stress tests. The three
// helpers here are standalone processes invoked by http_bench_test.go via
// subprocess flags; they are not meant to be run as normal unit tests.

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/quic-go/quic-go/http3"
)

// ----------------------------------------------------------- TCP echo ---

const (
	NAMETCP = "TCP"
)

func TestEchoTCP(t *testing.T) {
	listen := "0.0.0.0:18095"
	if len(os.Args) > 5 {
		listen = os.Args[len(os.Args)-1]
	}

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Println(NAMETCP+" listener err: ", err)
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println(NAMETCP+" read error: ", err)
			return
		}

		fmt.Println(NAMETCP+" read buffer: ", string(buffer), ", n: ", n)
		message := string(buffer[:n])
		response := fmt.Sprintf(NAMETCP+"recv: %s", message)
		_, err = conn.Write([]byte(response))
		if err != nil {
			fmt.Println(NAMETCP+" send error: ", err)
			return
		}
		fmt.Println(NAMETCP+" send buffer: ", string(message))
	}
}

// ----------------------------------------------------------- HTTP/1 ----

func TestHTTPServer(t *testing.T) {
	listen := "0.0.0.0:18091"

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from HTTP1 server"))
	})

	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		w.WriteHeader(http.StatusOK)
		if len(body) > 0 {
			w.Write(body)
		} else {
			// Fallback to query param if body is empty
			w.Write([]byte(r.URL.Query().Get("data")))
		}
	})

	fmt.Printf("HTTP1 server listening on %s\n", listen)
	if err := http.ListenAndServe(listen, nil); err != nil {
		t.Fatalf("HTTP server failed to start: %v", err)
	}
}

// ----------------------------------------------------------- HTTP/3 ----

func TestHTTP3Client(t *testing.T) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal(err)
	}
	roundTripper := &http3.RoundTripper{
		TLSClientConfig: &tls.Config{
			RootCAs:            pool,
			InsecureSkipVerify: true,
		},
	}
	defer roundTripper.Close()
	hclient := &http.Client{
		Transport: roundTripper,
	}
	rsp, err := hclient.Get("https://127.0.0.1:18093/")
	if err != nil {
		t.Fatal(err)
	}
	body := &bytes.Buffer{}
	r, err := io.Copy(body, rsp.Body)
	if err != nil {
		t.Fatal(err)
	} else {
		t.Fatal("r: ", r, ", body: ", body.String())
	}
}

// curl -i -XPUT http://127.0.0.1:18093 -k -d hello
