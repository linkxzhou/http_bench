# http_bench - 简单的HTTP压测工具

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
./http_bench -n 1000 -c 10 -t 3000 -m GET http://www.baidu.com/

发送1000请求, 同时打开10个client, 超时时间设置为3000ms，请求方式为GET，请求链接http://www.baidu.com/

Output:
    Request:
    [1000] http://www.baidu.com
    Summary:
    Total:        5.2124 secs
    Slowest:      0.3283 secs
    Fastest:      0.0195 secs
    Average:      0.0345 secs
    Requests/sec: 191.8491

    Status code distribution:
    [200] 1000 responses

    Latency distribution:
    10% in 0.0253 secs
    25% in 0.0272 secs
    50% in 0.0298 secs
    75% in 0.0350 secs
    90% in 0.0498 secs
    95% in 0.0606 secs
    99% in 0.0872 secs
```

## 命令行解析

```
    -n  请求HTTP的次数
    -c  并发的客户端数量，但是不能大于HTTP的请求次数
    -q  频率限制，每秒的请求数
    -o  输出结果格式，可以为CSV，也可以直接打印
    -m  HTTP方法，包括GET, POST, PUT, DELETE, HEAD, OPTIONS.
    -H  请求发起的HTTP的头部信息，例如：-H "Accept: text/html" -H "Content-Type: application/xml"
    -t  请求超时的毫秒
    -A  HTTP的Accept的头部字段
    -d  HTTP发起POST请求的body数据
    -T  HTTP的Content-type, 例如："text/html"，"application/json"
    -a  HTTP的鉴权请求, 例如：http://username:password@xxx/
    -x  HTTP的代理IP和端口
    -disable-compression  不启用压缩
    -disable-keepalive    不开启keepalive
    -cpus                 使用cpu的内核数
    -host                 HTTP请求的host的值
    -file  读取文件中的URL，格式为一行一个URL，发起请求每次随机选择发送的URL
```

测试命令行 : ./http_bench -n 1000 -c 10 -t 3000 -m GET -file urls.txt