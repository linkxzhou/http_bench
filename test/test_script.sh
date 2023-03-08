#!/bin/bash

GO=go # build go version
duration=5 # server test duration

function start_go_process() {
    echo "start process --> "$1
    if [ -n "$3" ]; then
        nohup $GO test -timeout=$[$3*$duration]s -v $1 -args "$1" $2 2>&1 & 
    else
        nohup $GO test -timeout=${duration}s -v $1 -args "$1" $2 2>&1 & 
    fi
}

function sleep_process() {
    if [ -n "$1" ]; then
        sleep ${1}
    else
        sleep 1
    fi
}

listen="127.0.0.1:18090"

echo "================= single stress test http/1"
# 1. start http1 server
start_go_process echo_http1_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d ${duration}s -http http1 -m GET -url "http://$listen/"
sleep_process
echo "[PASS] http/1"

echo "================= single stress test http/2"
# 1. start http2 server
start_go_process echo_http2_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d ${duration}s -http http2 -m GET -url "https://$listen/"
sleep_process
echo "[PASS] http/2"

echo "================= single stress test http/3"
# 1. start http3 server
start_go_process echo_http3_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d ${duration}s -http http3 -m GET -url "https://$listen/"
sleep_process
echo "[PASS] http/3"

echo "================= single stress test ws"
# 1. start ws server
start_go_process echo_ws_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d ${duration}s -http ws -m GET -url "ws://$listen/"
sleep_process
echo "[PASS] WS"

echo "================= single stress test http/1 urls"
url_counts=8
# 1. start http1 server for urls
start_go_process echo_http1_test.go $listen $url_counts
# 2. start stress test for urls
$GO run ../http_bench.go -c 1 -d ${duration}s -http http1 -m GET -url-file test_urls.txt
sleep_process $url_counts
echo "[PASS] http/1 for urls"