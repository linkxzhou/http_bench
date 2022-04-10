package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	listen := "127.0.0.1:18090"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	})
	fmt.Fprintf(os.Stdout, "Server listen %s\n", listen)
	if err := http.ListenAndServe(listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, "ListenAndServe err: %s\n", err.Error())
	}
}
