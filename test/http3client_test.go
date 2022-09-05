package test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"testing"

	"github.com/lucas-clemente/quic-go/http3"
)

func TestHTTP3Client(t *testing.T) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}
	body := &bytes.Buffer{}
	r, err := io.Copy(body, rsp.Body)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Fatal("==== r: ", r, ", body: ", body.String())
	}
}

// curl -i -XPUT http://127.0.0.1:18093 -k -d hello
