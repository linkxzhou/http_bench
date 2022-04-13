// openssl req -newkey rsa:2048 -nodes -keyout server.key -x509 -days 365 -out server.crt
package test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

const (
	NAME2 = "HTTP2.0"
)

func TestEchoHTTP2(t *testing.T) {
	listen := "0.0.0.0:19090"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`This is ` + NAME2 + ` Echo Server`))
	})
	srv := &http.Server{
		Addr: listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`This is ` + NAME2 + ` Echo Server`))
		}),
	}
	fmt.Fprintf(os.Stdout, NAME2+" Server listen %s\n", listen)
	if err := srv.ListenAndServeTLS("server.crt", "server.key"); err != nil {
		fmt.Fprintf(os.Stderr, NAME2+" ListenAndServe err: %s\n", err.Error())
	}
}

// curl -i -XPUT --http2 https://127.0.0.1:19090 -k -d hello
