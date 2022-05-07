#!/bin/sh

function start_go_process() {
    echo "start process --> "$1
    nohup go test -v $1 2>&1 >/dev/null &
    return $!
}

function kill_go_process() {
    echo "stop process --> "$1
    kill -9 $1
}

echo "================= single stress test http/1"
# 1. start http1 server
pid=`start_go_process echo_http1_test.go`
# 2. start stress test
go run ../http_bench.go -c 1 -d 10s -m GET -url "http://127.0.0.1:18090/test1"
# 3. stop http1 server
kill_go_process $pid