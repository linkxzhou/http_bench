package test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"testing"

	"github.com/quic-go/quic-go/http3"
)

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
