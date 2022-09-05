package test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

const (
	NAME1 = "HTTP1.1"
)

func TestEchoHTTP1(t *testing.T) {
	listen := "0.0.0.0:18091"
	if len(os.Args) > 4 {
		listen = os.Args[len(os.Args)-1]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`This is ` + NAME1 + ` Echo Server`))
	})
	fmt.Fprintf(os.Stdout, NAME1+" Server listen %s\n", listen)
	if err := http.ListenAndServe(listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, NAME1+" ListenAndServe err: %s\n", err.Error())
	}
}

// curl -i -XPUT http://127.0.0.1:18091 -k -d hello
