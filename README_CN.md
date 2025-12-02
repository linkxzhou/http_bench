# HTTP Bench - 强大的 HTTP 压力测试工具

[![build](https://github.com/linkxzhou/http_bench/actions/workflows/build1.20.yml/badge.svg)](https://github.com/linkxzhou/http_bench/actions/workflows/build1.20.yml)
[![build](https://github.com/linkxzhou/http_bench/actions/workflows/build1.21.yml/badge.svg)](https://github.com/linkxzhou/http_bench/actions/workflows/build1.21.yml)
[![build](https://github.com/linkxzhou/http_bench/actions/workflows/build1.22.yml/badge.svg)](https://github.com/linkxzhou/http_bench/actions/workflows/build1.22.yml)

**HTTP Bench** 是一个轻量级、高性能的压力测试工具，支持多种协议和分布式测试能力。

[English Document](https://github.com/linkxzhou/http_bench/blob/master/README.md)     
[中文文档](https://github.com/linkxzhou/http_bench/blob/master/README_CN.md)     
[使用样例](https://github.com/linkxzhou/http_bench/blob/master/EXAMPLE_CN.md)   

## 功能特点

- ✅ **多协议支持**：HTTP/1、HTTP/2、HTTP/3、WebSocket 和 gRPC（即将推出）
- ✅ **分布式测试**：跨多台机器运行测试，实现更高负载
- ✅ **模板函数**：使用内置函数和变量动态生成请求
- ✅ **Web 仪表盘**：通过浏览器界面监控和控制测试
- ✅ **全面的指标**：详细的性能统计和延迟分布
- ✅ **灵活配置**：丰富的命令行选项，可自定义测试场景

![仪表盘演示](./docs/httpbench_cn.png)

## 安装

**要求：Go 版本 1.20 或更高**

### 方式一：使用 Go Get

```bash
go get github.com/linkxzhou/http_bench
```

### 方式二：从源码构建

```bash
git clone git@github.com:linkxzhou/http_bench.git
cd http_bench
go build .
```

## 架构

以下图表说明了 HTTP Bench 的架构：

![架构图](./docs/arch.png)

## 基本用法

运行一个简单的压力测试，使用 1000 个并发连接持续 60 秒：

```bash
./http_bench http://127.0.0.1:8000 -c 1000 -d 60s
```

输出示例：

```
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

## 命令行选项

```
-n  请求 HTTP 的次数
-c  并发的客户端数量（不能大于 HTTP 的请求次数）
-q  频率限制，每秒的请求数 (QPS)
-d  压测持续时间（例如：2s, 2m, 2h）
-t  请求超时时间（毫秒）
-o  输出类型（默认：摘要，可选：'csv'）
-m  HTTP 方法（GET, POST, PUT, DELETE, HEAD, OPTIONS）
-H  自定义 HTTP 头部（例如：-H "Accept: text/html" -H "Content-Type: application/xml"）
-http  支持 http1, http2, http3, ws, wss，默认 http1
-body  HTTP 请求体，默认为空
-a  HTTP 基本认证，格式：username:password
-x  HTTP 代理地址，格式：host:port
-disable-compression  禁用压缩
-disable-keepalive    禁用 keep-alive，防止在不同 HTTP 请求之间重用 TCP 连接
-cpus     使用的 CPU 核心数（默认为当前机器的核心数）
-url      请求单个 URL
-verbose  打印详细日志，默认级别：2（0:TRACE, 1:DEBUG, 2:INFO ~ ERROR）
-url-file 从文件读取 URL 列表并随机压测
-body-file 从文件读取请求体
-listen   监听 IP:PORT 用于分布式压测和工作机器（默认为空）。例如："127.0.0.1:12710"
-dashboard 监听仪表盘 IP:PORT 并在浏览器上操作压测参数
-W  运行分布式压测的工作机器列表
      例如：-W "127.0.0.1:12710" -W "127.0.0.1:12711"
-example  打印压测示例（默认为 false）
```

## 使用示例

### 单 URL 测试

```bash
# 基本 GET 请求，1000 次请求，10 个并发连接
./http_bench -n 1000 -c 10 -m GET -url "http://127.0.0.1/test1"
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1"
```

### 从文件测试 URL 列表

```bash
# 从文件随机选择 URL，1000 次请求，10 个并发连接
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1/test1" -url-file urls.txt

# 基于持续时间的测试，使用 POST 请求和请求体
./http_bench -d 10s -c 10 -m POST -body "{}" -url-file urls.txt
```

### HTTP/2 测试

```bash
./http_bench -d 10s -c 10 -http http2 -m POST "http://127.0.0.1/test1" -body "{}"
```

### HTTP/3 测试

```bash
./http_bench -d 10s -c 10 -http http3 -m POST "http://127.0.0.1/test1" -body "{}"
```

### WebSocket 测试

```bash
./http_bench -d 10s -c 10 -http ws "ws://127.0.0.1" -body "{}"
```

### 分布式压力测试

```bash
# 步骤 1：在不同机器上启动工作实例
./http_bench -listen "127.0.0.1:12710" -verbose 1
./http_bench -listen "127.0.0.1:12711" -verbose 1

# 步骤 2：运行控制器协调测试
./http_bench -c 1 -d 10s "http://127.0.0.1:18090/test1" -body "{}" -W "127.0.0.1:12710" -W "127.0.0.1:12711" -verbose 1
```

### Web 仪表盘

```bash
# 步骤 1：启动仪表盘服务器
./http_bench -dashboard "127.0.0.1:12345" -verbose 1

# 步骤 2：在浏览器中打开仪表盘 URL
# http://127.0.0.1:12345
```

## 模板函数和变量

HTTP Bench 支持使用模板函数动态生成请求。这些函数可以在 URL 参数和请求体中使用。

### 1. 整数求和

计算多个整数的和。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ intSum 1 2 3 4}}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ intSum 1 2 3 4 }}" -verbose 0
```

### 2. 随机整数

生成介于最小值和最大值之间的随机整数。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ random 1 100000}}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ random 1 100000 }}" -verbose 0
```

### 3. 随机日期

生成指定格式的随机日期字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomDate 'YMD' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomDate 'YMD' }}" -verbose 0
```

### 4. 随机字符串

生成指定长度的随机字母数字字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomString 10}}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomString 10 }}" -verbose 0
```

### 5. 随机数字字符串

生成指定长度的随机数字字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomNum 10}}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomNum 10 }}" -verbose 0
```

### 6. 当前日期

输出指定格式的当前日期。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ date 'YMD' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ date 'YMD' }}" -verbose 0
```

### 7. UUID

生成 UUID。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ UUID | escape }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ UUID }}" -verbose 0
```

### 8. 字符串转义

转义字符串中的特殊字符。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ UUID | escape }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ UUID | escape }}" -verbose 0
```

### 9. 十六进制转字符串

将十六进制字符串转换为普通字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ hexToString '68656c6c6f20776f726c64' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ hexToString '68656c6c6f20776f726c64' }}" -verbose 0
```

### 10. 字符串转十六进制

将字符串转换为其十六进制表示。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ stringToHex 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ stringToHex 'hello world' }}" -verbose 0
```

### 11. 转为字符串

将值转换为带引号的字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomNum 10 | toString }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomNum 10 | toString }}" -verbose 0
```

### 12. Base64 编码

将字符串编码为 Base64 格式。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ base64Encode 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ base64Encode 'hello world' }}" -verbose 0
```

### 13. Base64 解码

解码 Base64 编码的字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ base64Decode 'aGVsbG8gd29ybGQ=' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ base64Decode 'aGVsbG8gd29ybGQ=' }}" -verbose 0
```

### 14. MD5 哈希

生成字符串的 MD5 哈希值。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ md5 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ md5 'hello world' }}" -verbose 0
```

### 15. SHA1 哈希

生成字符串的 SHA1 哈希值。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ sha1 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ sha1 'hello world' }}" -verbose 0
```

### 16. SHA256 哈希

生成字符串的 SHA256 哈希值。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ sha256 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ sha256 'hello world' }}" -verbose 0
```

### 17. HMAC 签名

使用指定的哈希算法生成 HMAC 签名。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ hmac 'secret_key' 'message' 'sha256' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ hmac 'secret_key' 'message' 'sha256' }}" -verbose 0
```

### 18. 随机 IP 地址

生成随机的 IP 地址。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomIP }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomIP }}" -verbose 0
```

### 19. 字符串截取

从字符串中提取子字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ substring 'hello world' 0 5 }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ substring 'hello world' 0 5 }}" -verbose 0
```

### 20. 字符串替换

将字符串中所有出现的子字符串替换为另一个字符串。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ replace 'hello world' 'world' 'golang' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ replace 'hello world' 'world' 'golang' }}" -verbose 0
```

### 21. 转为大写

将字符串转换为大写。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ upper 'hello world' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ upper 'hello world' }}" -verbose 0
```

### 22. 转为小写

将字符串转换为小写。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ lower 'HELLO WORLD' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ lower 'HELLO WORLD' }}" -verbose 0
```

### 23. 去除空格

去除字符串首尾的空白字符。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ trim '  hello world  ' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ trim '  hello world  ' }}" -verbose 0
```

### 24. 随机选择

从多个选项中随机选择一个。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomChoice 'apple' 'banana' 'cherry' }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomChoice 'apple' 'banana' 'cherry' }}" -verbose 0
```

### 25. 随机浮点数

生成介于最小值和最大值之间的随机浮点数。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomFloat 1.5 10.5 }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomFloat 1.5 10.5 }}" -verbose 0
```

### 26. 随机布尔值

生成随机的布尔值（true 或 false）。

```bash
# URL 参数
./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomBoolean }}" -verbose 0

# 请求体
./http_bench -c 1 -n 1 "https://127.0.0.1:18090" -body "data={{ randomBoolean }}" -verbose 0
```

### macOS Catalina 构建错误

如果遇到错误 `pointer is missing a nullability type specifier when building on catalina`，请使用以下解决方法：

```bash
export CGO_CPPFLAGS="-Wno-error -Wno-nullability-completeness -Wno-expansion-to-defined"
```

## 贡献

欢迎贡献！请随时提出问题或提交拉取请求。

## 许可证

本项目采用 MIT 许可证 - 详情请参阅 LICENSE 文件。