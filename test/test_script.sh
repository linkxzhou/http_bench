#!/bin/sh

GO=go

function start_go_process() {
    echo "start process --> "$1
    nohup $GO test -v $1 -args $2 2>&1 >/dev/null & echo $! > command.pid
}

function kill_go_process() {
    echo "stop process --> "`cat command.pid`
    kill `cat command.pid`
    sleep 2
}

listen="127.0.0.1:18090"
echo "================= single stress test http/1"
# 1. start http1 server
start_go_process echo_http1_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d 2s -http http1 -m GET -url "http://$listen/"
# 3. stop http1 server
kill_go_process

echo "================= single stress test http/2"
# 1. start http2 server
start_go_process echo_http2_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d 2s -http http2 -m GET -url "https://$listen/"
# 3. stop http2 server
kill_go_process

echo "================= single stress test http/3"
# 1. start http3 server
start_go_process echo_http3_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d 2s -http http3 -m GET -url "https://$listen/"
# 3. stop http3 server
kill_go_process

echo "================= single stress test ws"
# 1. start ws server
start_go_process echo_ws_test.go $listen
# 2. start stress test
$GO run ../http_bench.go -c 1 -d 2s -http ws -m GET -url "ws://$listen/"
# 3. stop ws server
kill_go_process