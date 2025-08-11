package main

import (
	"bytes"
	"fmt"
	"sync"
	"text/template"
	"time"
)

type HttpbenchWorker struct {
	stopChan          chan bool
	isStop            bool
	result            *CollectResult // current worker result
	err               error
	urlTmpl, bodyTmpl *template.Template
}

var hbWorkerList sync.Map

func NewWorker(seqId int64) *HttpbenchWorker {
	var hbWorker *HttpbenchWorker

	if v, ok := hbWorkerList.Load(seqId); ok && v != nil {
		hbWorker = v.(*HttpbenchWorker)
		verbosePrint(logLevelInfo, "worker %d already exists!!!", seqId)
	} else {
		hbWorker = &HttpbenchWorker{}
		hbWorkerList.Store(seqId, hbWorker)
		verbosePrint(logLevelInfo, "worker %d created!!!", seqId)
	}

	return hbWorker
}

func (w *HttpbenchWorker) Start(params HttpbenchParameters) *CollectResult {
	w.result = NewCollectResult()
	w.stopChan = make(chan bool, 1000)
	if params.Duration <= 0 {
		params.Duration = defaultWorkerTimeout
	}

	// start time
	startTime := time.Now()

	// do http bench worker
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			verbosePrint(logLevelDebug, "client finish!!!")
			w.Stop()
			wg.Done()
		}()

		w.do(params)
	}()

	// wait stop signal or timeout
	select {
	case isStop, isok := <-w.stopChan:
		if isok && isStop {
			verbosePrint(logLevelDebug, "client stop!!!")
			w.Stop()
		}
	case <-time.After(time.Duration(params.Duration) * time.Millisecond):
		verbosePrint(logLevelDebug, "client timeout!!!")
		w.Stop()
	}

	w.result.Duration = int64(time.Since(startTime).Seconds())
	verbosePrint(logLevelInfo, "worker finished and waiting result")
	wg.Wait()
	w.result = mergeCollectResult(nil, w.result)
	return w.result
}

// Stop stop stress worker and wait coroutine finish
func (w *HttpbenchWorker) Stop() error {
	w.isStop = true
	w.stopChan <- true
	return w.err
}

func (w *HttpbenchWorker) GetResult() *CollectResult {
	if w.isStop {
		w.result.ErrCode = 1
		w.result.ErrMsg = "http_bench stopped"
	}
	return w.result
}

func (w *HttpbenchWorker) do(params HttpbenchParameters) error {
	clientNum := params.C

	println("[%v][%v] running %d connections and duration %d secs, @ %s",
		params.RequestType, params.RequestMethod, clientNum, params.Duration/1000, params.Url)

	var (
		wg               sync.WaitGroup
		err              error
		bodyTemplateName = fmt.Sprintf("HttpbenchBODY-%d", params.SequenceId)
		urlTemplateName  = fmt.Sprintf("HttpbenchURL-%d", params.SequenceId)

		// Initialize connection pool with proper size limit
		connPool = NewClientPool(clientNum)
	)

	w.urlTmpl, err = template.New(urlTemplateName).Funcs(fnMap).Parse(params.Url)
	if err != nil {
		verbosePrint(logLevelError, "parse urls function err: %v", err)
		return err
	}
	verbosePrint(logLevelDebug, "parse urls: %s", params.Url)

	w.bodyTmpl, err = template.New(bodyTemplateName).Funcs(fnMap).Parse(params.RequestBody)
	if err != nil {
		verbosePrint(logLevelError, "parse request body function err: %v", err)
		return err
	}

	timeInterval := 0
	if params.Qps > 0 {
		timeInterval = 1e6 / (clientNum * params.Qps)
	}
	reqNumWorkerPer := params.N / clientNum

	for i := 0; i < clientNum; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			client := connPool.Get()
			if client == nil {
				verbosePrint(logLevelError, "client is nil")
				return
			}

			err := client.Init(ClientOpts{
				typ:    params.RequestType,
				params: params,
			})
			if err != nil {
				verbosePrint(logLevelError, "client init err: ", err)
				return
			}

			defer func() {
				connPool.Put(client)
				if r := recover(); r != nil {
					verbosePrint(logLevelError, "internal err: %v", r)
				}
			}()

			w.doClient(client, reqNumWorkerPer, timeInterval)
		}()
	}

	wg.Wait()
	return nil
}

func (w *HttpbenchWorker) doClient(client *Client, n, sleep int) {
	var runCounts int = 0

	for !w.isStop && (n <= 0 || runCounts < n) {
		runCounts++
		if sleep > 0 {
			time.Sleep(time.Duration(sleep) * time.Microsecond)
		}

		// Execute template
		urlBuf := &bytes.Buffer{}
		if err := w.urlTmpl.Execute(urlBuf, nil); err != nil {
			verbosePrint(logLevelError, "execute url template err: %v", err)
			return
		}

		bodyBuf := &bytes.Buffer{}
		if err := w.bodyTmpl.Execute(bodyBuf, nil); err != nil {
			verbosePrint(logLevelError, "execute body template err: %v", err)
			return
		}

		verbosePrint(logLevelDebug, "url: %s, body: %s", urlBuf.String(), bodyBuf.String())

		t := time.Now()
		code, size, err := client.Do(urlBuf.Bytes(), bodyBuf.Bytes(), 0)
		verbosePrint(logLevelTrace, "runCounts: %d, code: %d, size: %d, err: %v",
			runCounts, code, size, err)

		w.result.append(&result{
			statusCode:    code,
			duration:      time.Since(t),
			contentLength: size,
			err:           err,
		})

		if err != nil {
			verbosePrint(logLevelWarn, "request err: %v", err)
			if w.result.isCircuitBreak() {
				verbosePrint(logLevelError, "error rate exceeded 50%% and circuit break!!!")
				return
			}
		}
	}
}
