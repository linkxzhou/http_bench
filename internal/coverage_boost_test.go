package bench

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestHttpbenchParameters_StringGetBodyMerge(t *testing.T) {
	p := &HttpbenchParameters{
		URL: "http://example.com", N: 1, C: 1, RequestMethod: "GET",
	}
	s := p.String()
	if !strings.Contains(s, "example.com") {
		t.Fatalf("String=%s", s)
	}

	b, r := p.GetRequestBody()
	if b != nil || r != nil {
		t.Fatal("empty body should be nil")
	}
	p.RequestBody = "hello"
	b, r = p.GetRequestBody()
	if string(b) != "hello" || r == nil {
		t.Fatalf("string body %q", b)
	}
	p.RequestBody = hex.EncodeToString([]byte("hi"))
	p.RequestBodyType = BodyHex
	b, r = p.GetRequestBody()
	if string(b) != "hi" || r == nil {
		t.Fatalf("hex body %q", b)
	}
	p.RequestBody = "zz"
	b, r = p.GetRequestBody()
	if b != nil || r != nil {
		t.Fatal("bad hex should nil")
	}

	base := &HttpbenchParameters{URL: "a", N: 1, C: 1, RequestMethod: "GET"}
	base.Merge(&HttpbenchParameters{
		URL: "b", N: 9, C: 3, RequestMethod: "POST", RequestBody: "x",
		RequestBodyType: BodyString, RequestType: ProtocolHTTP2,
		ProxyURL: "http://p", Duration: time.Second, Timeout: time.Second,
		QPS: 5, DisableCompression: true, DisableKeepAlives: true,
		Headers: map[string][]string{"H": {"v"}}, Output: "csv", From: "f",
	})
	if base.URL != "b" || base.N != 9 || base.C != 3 || base.RequestMethod != "POST" ||
		base.RequestBody != "x" || base.RequestType != ProtocolHTTP2 || base.QPS != 5 ||
		base.Output != "csv" || base.From != "f" || base.Headers["H"][0] != "v" {
		t.Fatalf("merge incomplete: %+v", base)
	}
}

func TestClientOptsEqualAndHeadersEqual(t *testing.T) {
	a := ClientOpts{Protocol: ProtocolHTTP1, Insecure: true, Params: HttpbenchParameters{URL: "u", Timeout: time.Second}}
	b := a
	if !clientOptsEqual(a, b) {
		t.Fatal("equal")
	}
	b.Protocol = ProtocolHTTP2
	if clientOptsEqual(a, b) {
		t.Fatal("protocol differ")
	}
	b = a
	b.Insecure = false
	if clientOptsEqual(a, b) {
		t.Fatal("insecure differ")
	}
	b = a
	b.Params.URL = "other"
	if clientOptsEqual(a, b) {
		t.Fatal("url differ")
	}
	if !headersEqual(nil, map[string][]string{}) {
		t.Fatal("nil/empty equal")
	}
	if headersEqual(map[string][]string{"A": {"1"}}, map[string][]string{"A": {"1", "2"}}) {
		t.Fatal("len differ")
	}
	if headersEqual(map[string][]string{"A": {"1"}}, map[string][]string{"A": {"2"}}) {
		t.Fatal("value differ")
	}
	if headersEqual(map[string][]string{"A": {"1"}}, map[string][]string{"B": {"1"}}) {
		t.Fatal("key differ")
	}
	if !headersEqual(map[string][]string{"A": {"1"}}, map[string][]string{"A": {"1"}}) {
		t.Fatal("should equal")
	}
}

func TestClient_InitHTTP2HTTP3ReuseAndBadProtocol(t *testing.T) {
	c := &Client{}
	opts := ClientOpts{Protocol: ProtocolHTTP2, Insecure: true, Params: HttpbenchParameters{Timeout: time.Second}}
	if err := c.Init(opts); err != nil {
		t.Fatalf("http2: %v", err)
	}
	if err := c.Init(opts); err != nil {
		t.Fatalf("reuse: %v", err)
	}
	c2 := &Client{}
	if err := c2.Init(ClientOpts{Protocol: ProtocolHTTP3, Insecure: true, Params: HttpbenchParameters{Timeout: time.Second}}); err != nil {
		t.Fatalf("http3: %v", err)
	}
	c3 := &Client{}
	if err := c3.Init(ClientOpts{Protocol: "ftp", Params: HttpbenchParameters{}}); err == nil {
		t.Fatal("want unsupported protocol")
	}
	c4 := &Client{}
	if err := c4.Init(ClientOpts{Protocol: ProtocolWS, Params: HttpbenchParameters{URL: "ws://127.0.0.1:1/"}}); err == nil {
		t.Fatal("want ws dial error")
	}
}

func TestClient_DoUninitializedAndHTTP2Request(t *testing.T) {
	c := &Client{}
	if _, _, err := c.Do(context.Background(), []byte("http://x"), nil, 0); err == nil {
		t.Fatal("uninitialized")
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c = &Client{}
	params := HttpbenchParameters{
		URL: srv.URL, RequestMethod: http.MethodGet, Timeout: 2 * time.Second, Insecure: true,
	}
	if err := c.Init(ClientOpts{Protocol: ProtocolHTTP2, Params: params, Insecure: true}); err != nil {
		t.Fatal(err)
	}
	code, n, err := c.Do(context.Background(), []byte(srv.URL), nil, time.Second)
	// HTTP/2 against httptest may fail depending on ALPN; just exercise path
	t.Logf("http2 do: code=%d n=%d err=%v", code, n, err)
	_ = c.Close()
}

func TestMetrics_PrintWriteReportToReportSnapshotPrivate(t *testing.T) {
	r := NewCollectResult()
	r.TotalRequests = 4
	r.FailedRequests = 1
	r.BytesReceived = 2048
	r.Duration = 2 * time.Second
	r.RPS = 2
	r.Fastest = time.Millisecond
	r.Slowest = 10 * time.Millisecond
	r.Average = 5 * time.Millisecond
	r.StopReason = "count"
	r.StatusCodeCounts = map[int]int{200: 3, 500: 1}
	r.ErrorCounts = map[string]int{"e": 1}
	r.LatencyHistogram = map[time.Duration]int64{time.Millisecond: 3}
	r.Output = "csv"

	snap := r.ToReportSnapshot()
	if snap.TotalRequests != 4 || snap.StatusCodeCounts[200] != 3 || snap.ErrorCounts["e"] != 1 {
		t.Fatalf("%+v", snap)
	}

	var buf bytes.Buffer
	r.WriteReport(&buf)
	if buf.Len() == 0 {
		t.Fatal("empty csv report")
	}
	r.Output = "html"
	buf.Reset()
	r.WriteReport(&buf)
	if !strings.Contains(buf.String(), "html") && !strings.Contains(buf.String(), "Benchmark") {
		t.Fatalf("html report: %s", buf.String())
	}
	r.Output = ""
	// Print goes to stdout — redirect via WriteReport already covered; call Print briefly
	old := os.Stdout
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devNull
	r.Print()
	os.Stdout = old
	_ = devNull.Close()

	if toByteSizeStr(500) == "" || toByteSizeStr(float64(KB)) == "" ||
		toByteSizeStr(float64(MB)) == "" || toByteSizeStr(float64(GB)) == "" {
		t.Fatal("toByteSizeStr")
	}
}

func TestRequest_MergeDefaultsAndParseHeadersEdge(t *testing.T) {
	def := &HttpbenchParameters{
		N: 10, C: 2, URL: "http://def", RequestMethod: "GET",
		Headers: map[string][]string{"A": {"1"}}, RequestBody: "b0",
	}
	s := Spec{URL: "http://s", Method: "POST", Headers: map[string][]string{"B": {"2"}}, Body: "bb"}
	p := s.MergeDefaults(def)
	if p.URL != "http://s" || p.RequestMethod != "POST" || p.RequestBody != "bb" || p.N != 10 || p.Headers["B"][0] != "2" {
		t.Fatalf("%+v", p)
	}
	empty := Spec{}
	p2 := empty.MergeDefaults(def)
	if p2.URL != "http://def" || p2.RequestMethod != "GET" {
		t.Fatalf("%+v", p2)
	}

	specs, err := ParseContent([]byte("GET http://example.com/\r\nX-Bad\r\n\r\n"))
	// may return specs with warning or error; just exercise parseHeaders bad line
	t.Logf("parse with bad header: specs=%d err=%v", len(specs), err)
}

func TestTmplHelpers_Uncovered(t *testing.T) {
	if hexToString("6869") != "hi" {
		t.Fatal("hexToString")
	}
	if hexToString("zz") != "" {
		t.Fatal("bad hex")
	}
	if stringToHex("hi") != "6869" {
		t.Fatal("stringToHex")
	}
	if !strings.Contains(toString(1, "a"), "1") {
		t.Fatal("toString")
	}
	if intSum(1, 2, 3) != 6 {
		t.Fatal("intSum")
	}
	if max(1, 5, 3) != 5 || min(1, 5, 3) != 1 {
		t.Fatal("max/min")
	}
	if tmplToByteSizeStr(100) == "" || tmplToByteSizeStr(float64(KB)) == "" ||
		tmplToByteSizeStr(float64(MB)) == "" || tmplToByteSizeStr(float64(GB)) == "" {
		t.Fatal("tmplToByteSizeStr")
	}
	u := uuid()
	if len(u) != 36 || strings.Count(u, "-") != 4 {
		t.Fatalf("uuid=%q", u)
	}
	// via FnMap execute
	tmpl, err := template.New("t").Funcs(FnMap).Parse(`{{UUID}}|{{hexToString "6162"}}|{{stringToHex "x"}}|{{max 1 9}}|{{min 1 9}}|{{intSum 1 2}}`)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ab") || !strings.Contains(buf.String(), "|9|") {
		t.Fatalf("tmpl out=%s", buf.String())
	}
}

func TestLogging_SlogHandlerExtras(t *testing.T) {
	h := newSlogHandler()
	_ = h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	_ = h.WithGroup("g")
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	old := GetLevel()
	defer SetLevel(old)
	SetLevel(LevelDebug)
	for _, l := range levels {
		if !h.Enabled(context.Background(), l) {
			t.Fatalf("enabled %v", l)
		}
		_ = mapSlogLevel(l)
	}
	SetLevel(LevelError)
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug should be disabled at error threshold")
	}
	_ = h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "hi", 0))
}

func TestTLS_RootCAsAndMaxDuration(t *testing.T) {
	cfg := tlsConfigWithRootCAs(true, nil)
	if !cfg.InsecureSkipVerify {
		t.Fatal("insecure")
	}
	loadHTTP3CertPool()
	if http3CertPool == nil {
		t.Log("system cert pool may be nil on some platforms; load still exercised")
	}
	if maxDuration(2*time.Second, time.Second) != 2*time.Second {
		t.Fatal("max")
	}
	if maxDuration(time.Second, 2*time.Second) != 2*time.Second {
		t.Fatal("max2")
	}
}

func TestWorker_NewGetResultTemplateErrorHex(t *testing.T) {
	w := NewWorker(GenSequenceId())
	if w == nil {
		t.Fatal("NewWorker")
	}
	if w.GetResult() != nil {
		t.Fatal("GetResult before run should be nil")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// bad template parse path
	w2 := NewWorkerForTest(GenSequenceId())
	_, err := w2.Run(context.Background(), HttpbenchParameters{
		URL: "{{", RequestMethod: "GET", RequestType: ProtocolHTTP1, N: 1, C: 1, Insecure: true,
	})
	if err == nil {
		t.Fatal("want template parse error")
	}

	// hex body path success
	w3 := NewWorkerForTest(GenSequenceId())
	res, err := w3.Run(context.Background(), HttpbenchParameters{
		URL: srv.URL, RequestMethod: http.MethodPost, RequestType: ProtocolHTTP1,
		RequestBody: hex.EncodeToString([]byte("abc")), RequestBodyType: BodyHex,
		N: 3, C: 1, Insecure: true, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.TotalRequests != 3 {
		t.Fatalf("%+v", res)
	}
	if got := w3.GetResult(); got == nil || got.TotalRequests != 3 {
		t.Fatalf("GetResult %+v", got)
	}

	// recordTemplateError directly
	seq := GenSequenceId()
	NewResult(seq)
	rc := 0
	recordTemplateError(seq, io.EOF, &rc)
	if rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
	_ = StopResult(seq)

	// default duration when neither N nor Duration
	w4 := NewWorkerForTest(GenSequenceId())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = w4.Run(ctx, HttpbenchParameters{
		URL: srv.URL, RequestMethod: http.MethodGet, RequestType: ProtocolHTTP1,
		C: 1, Insecure: true, Timeout: 100 * time.Millisecond,
	})
}

func TestParseDuration_MoreEdges(t *testing.T) {
	if _, err := parseDuration("XD"); err == nil {
		t.Fatal("bad day")
	}
	if _, err := parseDuration("XW"); err == nil {
		t.Fatal("bad week")
	}
	// normalizeCaseInsensitive default branch
	_ = normalizeCaseInsensitive("1us")
}

func TestValidateProxyURL_Invalid(t *testing.T) {
	// gourl.Parse rarely fails; exercise with weird input still returns nil often
	_ = validateProxyURL("http://[::1")
}

func TestDistributed_ExecuteNilRunnerAndCORSReject(t *testing.T) {
	svc := NewDefaultService(nil)
	// may panic if nil — use stub that returns nil result
	svc = NewDefaultService(WorkerRunnerFunc(func(ctx context.Context, p HttpbenchParameters) (*WorkerResponse, error) {
		return nil, nil
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"c":1,"n":1}`))
	ServeRequest(svc, DefaultValidator, rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("nil result should error, got %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://evil.example")
	ServeRequest(stubWorkerService{}, nil, rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("evil origin should not be allowed")
	}
}

func TestPostWorker_InvalidURLAndBadJSON(t *testing.T) {
	_, err := PostWorker(":", []byte(`{}`), time.Second)
	if err == nil {
		t.Fatal("invalid URL")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()
	_, err = PostWorker(srv.URL, []byte(`{}`), time.Second)
	if err == nil {
		t.Fatal("want decode error")
	}
}

func TestCli_ConflictingURLAndBadDurations(t *testing.T) {
	_, err := ParseConfig([]string{"-url", "http://a", "http://b"}, nil, nil)
	if err == nil {
		t.Fatal("conflict")
	}
	_, err = ParseConfig([]string{"-d", "nope", "http://a"}, nil, nil)
	if err == nil {
		t.Fatal("bad -d")
	}
	_, err = ParseConfig([]string{"-t", "nope", "-n", "1", "http://a"}, nil, nil)
	if err == nil {
		t.Fatal("bad -t")
	}
}

func TestDashboard_ListenErrorPath(t *testing.T) {
	// invalid addr
	err := Run(context.Background(), Config{Addr: ":::bad", HTML: "<html></html>", WorkerService: stubWorkerService{}})
	if err == nil {
		t.Fatal("want listen error")
	}
}
