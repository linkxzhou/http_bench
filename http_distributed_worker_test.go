package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPostDistributedWorker_Success 验证 postDistributedWorker 能正确解析并返回结果
func TestPostDistributedWorker_Success(t *testing.T) {
	// 搭一个返回固定 JSON 的测试服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := CollectResult{ErrCode: 0, ErrMsg: "ok", Rps: 123}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 调用
	res, err := postDistributedWorker(srv.URL, []byte(`{"foo":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrCode != 0 || res.ErrMsg != "ok" || res.Rps != 123 {
		t.Errorf("got %+v; want ErrCode=0, ErrMsg='ok', Rps=123", res)
	}
}

// TestPostDistributedWorker_InvalidJSON 验证 JSON 解析失败时返回错误
func TestPostDistributedWorker_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not a json`))
	}))
	defer srv.Close()

	_, err := postDistributedWorker(srv.URL, nil)
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

// TestPostAllDistributedWorker_mergeCollectResult 验证多个 worker 的结果能合并
func TestPostAllDistributedWorker_mergeCollectResult(t *testing.T) {
	// 返回 ErrCode=1, size=10
	mkSrv := func(code, size int64) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			resp := CollectResult{ErrCode: int(code), ErrMsg: "", SizeTotal: size}
			data, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		})
		return httptest.NewServer(mux)
	}
	s1 := mkSrv(1, 10)
	defer s1.Close()
	s2 := mkSrv(2, 20)
	defer s2.Close()

	// 使用完整 URL，这样 postAllDistributedWorker 会识别并拼接 "/api"
	addrs := flagSlice{s1.URL, s2.URL}
	merged, err := postAllDistributedWorkers(addrs, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 合并逻辑默认取 max(slowest)、min(fastest)、sum(size) 等
	wantSize := int64(10 + 20)
	if merged.SizeTotal != wantSize {
		t.Errorf("merged.Rps = %d; want %d", merged.SizeTotal, wantSize)
	}
	// 最少错误码只做示例检查
	if merged.ErrCode != 0 {
		t.Errorf("merged.ErrCode = %d; want 0 (merge 时不应该沿用单点 ErrCode)", merged.ErrCode)
	}
}
