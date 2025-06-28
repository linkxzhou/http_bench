package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

func serveDistributedWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var params HttpbenchParameters
	var result *CollectResult
	var err error
	var reqStr []byte

	defer func() {
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}()

	if reqStr, err = io.ReadAll(r.Body); err != nil {
		verbosePrint(logLevelError, "read body err: %s", err.Error())
		return
	}

	if err = json.Unmarshal(reqStr, &params); err != nil {
		verbosePrint(logLevelError, "unmarshal body err: %s", err.Error())
		return
	}

	verbosePrint(logLevelDebug, "request params: %s", params.String())

	var hbWorker = NewWorker(params.SequenceId)
	if result, err = HttpBenchStartup(hbWorker, params); err != nil {
		verbosePrint(logLevelError, "err: %v", err)
		return
	}

	if result == nil {
		verbosePrint(logLevelError, "result is nil")
		return
	}

	var respStr []byte
	respStr, err = result.marshal()
	if err != nil {
		verbosePrint(logLevelError, "marshal result: %v", err)
		return
	}
	w.Write(respStr)
}

func postDistributedWorker(uri string, body []byte) (*CollectResult, error) {
	verbosePrint(logLevelDebug, "request body: %s", string(body))
	// default not timeout
	resp, err := http.Post(uri, httpContentTypeJSON, bytes.NewBuffer(body))
	if err != nil {
		verbosePrint(logLevelError, "executeWorkerReq addr(%s) err: %s", uri, err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	var result CollectResult
	respStr, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(respStr, &result)
	return &result, err
}

func postAllDistributedWorkers(workAddrs flagSlice, jsonParams []byte) (*CollectResult, error) {
	var wg sync.WaitGroup
	var resultList []*CollectResult

	for _, v := range workAddrs {
		wg.Add(1)

		addr := fmt.Sprintf("http://%s%s", v, httpWorkerApiPath)
		if strings.Contains(v, "http://") || strings.Contains(v, "https://") {
			addr = fmt.Sprintf("%s%s", v, httpWorkerApiPath)
		}

		verbosePrint(logLevelDebug, "request addr: %s", addr)

		go func(workerAddr string) {
			defer wg.Done()
			result, err := postDistributedWorker(workerAddr, jsonParams)
			if err == nil && result != nil {
				resultList = append(resultList, result)
			}
		}(addr)
	}

	wg.Wait()
	return mergeCollectResult(nil, resultList...), nil
}
