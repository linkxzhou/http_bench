// openssl req -newkey rsa:2048 -nodes -keyout server.key -x509 -days 365 -out server.crt
package test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/lucas-clemente/quic-go/http3"
)

const (
	NAME3 = "HTTP3.0"
)

func TestEchoHTTP3(t *testing.T) {
	listen := "0.0.0.0:18093"
	srv := &http3.Server{
		Addr: listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, _ := ioutil.ReadAll(r.Body)
			fmt.Println(`This is ` + NAME3 + ` Echo Server, s: ` + string(s))
			w.Write([]byte(`This is ` + NAME3 + ` Echo Server`))
		}),
	}
	fmt.Fprintf(os.Stdout, NAME3+" Server listen %s\n", listen)
	if err := srv.ListenAndServeTLS("server.crt", "server.key"); err != nil {
		fmt.Fprintf(os.Stderr, NAME3+" ListenAndServe err: %s\n", err.Error())
	}
}
