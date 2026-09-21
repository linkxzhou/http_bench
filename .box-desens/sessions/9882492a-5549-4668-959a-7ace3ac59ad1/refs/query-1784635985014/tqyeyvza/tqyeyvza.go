package test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

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
