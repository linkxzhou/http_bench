## HTTP(HTTP/1, HTTP/2, Websocket)压测工具，支持单机和分布式

[English Document](https://github.com/linkxzhou/http_bench/blob/master/README.md)  
[中文文档](https://github.com/linkxzhou/http_bench/blob/master/README_CN.md)  

- [x] HTTP/1 压测
- [x] HTTP/2 压测
- [x] 分布式压测
- [x] 支持函数
- [ ] 支持变量
- [ ] HTTP/3 压测
- [x] Websocket 压测
- [x] UI Metrics

![avatar](./demo.png)

### 安装

```
go get github.com/linkxzhou/http_bench
```
或者
```
git clone git@github.com:linkxzhou/http_bench.git
cd http_bench
go build http_bench.go
```

### 架构
![avatar](./arch.png)

### 使用

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

### 命令行解析

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
-http  支持http1,http2,ws和wss, 默认http1
-x  HTTP的代理IP和端口
-disable-compression  不启用压缩
-disable-keepalive    不开启keepalive
-cpus                 使用cpu的内核数
-url                  压测单个URL
-verbose              打印详细日志，默认等级：3(0:TRACE, 1:DEBUG, 2:INFO, 3:ERROR)
-url-file   读取文件中的URL，格式为一行一个URL，发起请求每次随机选择发送的URL
-body-file  从文件中读取请求的body数据
-listen 分布式压测任务机器监听IP:PORT，例如： "127.0.0.1:12710".
-webui 监听端口，浏览器发起压测和查看QPS曲线.
-W  分布式压测执行任务的机器列表，例如： -W "127.0.0.1:12710" -W "127.0.0.1:12711".
```

执行压测样例(使用"-verbose 1"打印详细日志):
```
./http_bench -n 1000 -c 10 -m GET -url "http://127.0.0.1/test1"
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1"
```

执行压测按照文件随机压测(使用"-verbose 1"打印详细日志):
```
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1" -url-file urls.txt
./http_bench -d10s -c 10 -m POST "http://127.0.0.1/test1" -body "{}" -url-file urls.txt
```

执行压测，使用http/2:
```
./http_bench -d 10s -c 10 -http http2 -m POST "http://127.0.0.1/test1" -body "{}"
```

分布式压测样例(使用"-verbose 1"打印详细日志):
```
(1) 第一步:
./http_bench -listen "127.0.0.1:12710" -verbose 1
./http_bench -listen "127.0.0.1:12711" -verbose 1

(2) 第二步:
./http_bench -c 1 -d 10s "http://127.0.0.1:18090/test1" -body "{}" -W "127.0.0.1:12710" -W "127.0.0.1:12711" -verbose 1
```

浏览器发起压测:
```
(1) 第一步:
./http_bench -webui 127.0.0.1:12345 -verbose 1

(2) 第二步:
在浏览器打开地址(http://127.0.0.1:12345)
```

## 函数和变量
**(1) 计算整数之和**  
```
Function: 
  intSum number1 number2 number3 ...

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ intSum 1 2 3 4}}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ intSum 1 2 3 4 }}" -verbose 0
```

**(2) 生成随机整数**  
```
Function: 
  random min_value max_value 

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ random 1 100000}}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ random 1 100000 }}" -verbose 0
```

**(3) 生成随机日期**  
```
Function: 
  randomDate format(random date string: YMD = yyyyMMdd, HMS = HHmmss, YMDHMS = yyyyMMdd-HHmmss)

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomDate \"YMD\"}}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomDate \"YMD\" }}" -verbose 0
```

**(4) 生成制定大小的随机字符串**  
```
Function: 
  randomString count(random string: 0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ)

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomString 10}}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomString 10 }}" -verbose 0
```

**(5) 生成制定大小的随机数字字符串**  
```
Function: 
  randomNum count(random string: 0123456789)

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomString 10}}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomString 10 }}" -verbose 0
```

**(6) 输出当前日期**  
```
Function: 
  date format(YMD = yyyyMMdd, HMS = HHmmss, YMDHMS = yyyyMMdd-HHmmss) 

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ date \"YMD\" }}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ date \"YMD\" }}" -verbose 0
```

**(7) UUID标识（如果异常返回一个唯一随机字符串）**  
```
Function: 
  UUID 

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ UUID | escape }}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ UUID }}" -verbose 0
```

**(8) 字符串转换**  
```
Function: 
  escape str(pipeline with other functions)

Example:  
== Client Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ UUID | escape }}" -verbose 0
== Body Request Example:
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ UUID | escape }}" -verbose 0
```