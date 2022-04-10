# http_bench - 简单的HTTP压测工具，支持单机和分布式

[English Document](https://github.com/linkxzhou/http_bench/blob/master/README.md)  
[中文文档](https://github.com/linkxzhou/http_bench/blob/master/README_CN.md)  
  
## 安装

```
go get github.com/linkxzhou/http_bench
```
或者
```
git clone git@github.com:linkxzhou/http_bench.git
cd http_bench
go build http_bench.go
```

## 使用

```
./http_bench http://127.0.0.1:8000 -c 1000 -d 60s
Running 1000 connections, @ http://127.0.0.1:8000

Summary:
  Total:        63.031 secs
  Slowest:      0.640 secs
  Fastest:      0.000 secs
  Average:      0.072 secs
  Requests/sec: 12132.423
  Total data:   8.237 GB
  Size/request: 11566 bytes

Status code distribution:
  [200] 764713 responses

Latency distribution:
  10% in 0.014 secs
  25% in 0.030 secs
  50% in 0.060 secs
  75% in 0.097 secs
  90% in 0.149 secs
  95% in 0.181 secs
  99% in 0.262 secs
```

## 命令行解析

```
-n  请求HTTP的次数
-c  并发的客户端数量，但是不能大于HTTP的请求次数
-q  频率限制，每秒的请求数
-d  压测持续时间，默认10秒，例如：2s, 2m, 2h（s:秒，m:分钟，h:小时）
-t  设置请求的超时时间，默认3s
-o  输出结果格式，可以为CSV，也可以直接打印
-m  HTTP方法，包括GET, POST, PUT, DELETE, HEAD, OPTIONS.
-H  请求发起的HTTP的头部信息，例如：-H "Accept: text/html" -H "Content-Type: application/xml"
-body  HTTP发起POST请求的body数据
-a  HTTP的鉴权请求, 例如：http://username:password@xxx/
-x  HTTP的代理IP和端口
-disable-compression  不启用压缩
-disable-keepalive    不开启keepalive
-cpus                 使用cpu的内核数
-url                  压测单个URL
-verbose 	          打印详细日志，默认不开启
-file   读取文件中的URL，格式为一行一个URL，发起请求每次随机选择发送的URL
-listen 分布式压测任务机器监听IP:PORT，例如： "127.0.0.1:12710".
-W	    分布式压测执行任务的机器列表，例如： -W "127.0.0.1:12710" -W "127.0.0.1:12711".
```

执行压测样例(使用"-verbose true"打印详细日志):
```
./http_bench -n 1000 -c 10 -m GET -url "http://127.0.0.1/test1"
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1"
```

执行压测按照文件随机压测(使用"-verbose true"打印详细日志):
```
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1" -file urls.txt
./http_bench -d10s -c 10 -m POST "http://127.0.0.1/test1" -body "{}" -file urls.txt
```

分布式压测样例(使用"-verbose true"打印详细日志):
```
(1) 第一步:
  ./http_bench -listen "127.0.0.1:12710" -verbose true
  ./http_bench -listen "127.0.0.1:12711" -verbose true
(2) 第二步:
  ./http_bench -c 1 -d 10s "http://127.0.0.1:18090/test1" -body "{}" -W "127.0.0.1:12710" -W "127.0.0.1:12711" -verbose true
```