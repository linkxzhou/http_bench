# Start HTTP/1 or HTTP/2 Server
```
go test -v echo_http1_test.go
go test -v echo_http2_test.go
```

# Stress test
```
../http_bench -d 10s -c 10 -m POST "http://127.0.0.1:18090" -body "{}"
../http_bench -d 10s -c 10 -http http2 -m POST "http://127.0.0.1:19090" -body "{}"
```