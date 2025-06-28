# HTTP Bench 压测示例文档

本文档提供了 HTTP Bench 工具的详细使用示例，涵盖各种测试场景和高级功能。

## 目录

1. [基础压测示例](#基础压测示例)
2. [HTTP协议测试](#http协议测试)
3. [WebSocket测试](#websocket测试)
4. [分布式压测](#分布式压测)
5. [模板函数使用](#模板函数使用)
6. [高级配置](#高级配置)
7. [性能调优](#性能调优)
8. [故障排除](#故障排除)

## 基础压测示例

### 1. 简单GET请求

```bash
# 发送1000个请求，10个并发连接
./http_bench -n 1000 -c 10 "http://127.0.0.1:8080/api/test"

# 指定请求方法
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1:8080/api/users"
```

### 2. 基于时间的压测

```bash
# 持续压测30秒
./http_bench -d 30s -c 50 "http://127.0.0.1:8080/api/test"

# 持续压测5分钟
./http_bench -d 5m -c 100 "http://127.0.0.1:8080/api/test"

# 持续压测1小时
./http_bench -d 1h -c 200 "http://127.0.0.1:8080/api/test"
```

### 3. QPS限制测试

```bash
# 限制每秒100个请求
./http_bench -d 60s -c 10 -q 100 "http://127.0.0.1:8080/api/test"

# 限制每秒500个请求，持续10分钟
./http_bench -d 10m -c 50 -q 500 "http://127.0.0.1:8080/api/test"
```

### 4. POST请求示例

```bash
# 简单POST请求
./http_bench -n 1000 -c 20 -m POST "http://127.0.0.1:8080/api/users" \
  -body '{"name":"test","email":"test@example.com"}'

# 带自定义头部的POST请求
./http_bench -n 500 -c 10 -m POST "http://127.0.0.1:8080/api/login" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer token123" \
  -body '{"username":"admin","password":"secret"}'
```

### 5. 从文件读取请求体

```bash
# 创建请求体文件
echo '{"data":"large payload content"}' > request_body.json

# 使用文件中的请求体
./http_bench -n 1000 -c 20 -m POST "http://127.0.0.1:8080/api/data" \
  -body-file request_body.json
```

### 6. URL列表测试

```bash
# 创建URL列表文件
cat > urls.txt << EOF
http://127.0.0.1:8080/api/users
http://127.0.0.1:8080/api/products
http://127.0.0.1:8080/api/orders
http://127.0.0.1:8080/api/categories
EOF

# 随机测试URL列表
./http_bench -n 2000 -c 30 -url-file urls.txt
```

## HTTP协议测试

### 1. HTTP/1.1 测试（默认）

```bash
./http_bench -d 60s -c 50 -http http1 "http://127.0.0.1:8080/api/test"
```

### 2. HTTP/2 测试

```bash
# HTTP/2 GET请求
./http_bench -d 30s -c 20 -http http2 "https://127.0.0.1:8443/api/test"

# HTTP/2 POST请求
./http_bench -d 60s -c 30 -http http2 -m POST "https://127.0.0.1:8443/api/data" \
  -body '{"message":"HTTP/2 test"}'
```

### 3. HTTP/3 测试

```bash
# HTTP/3 测试（需要服务器支持QUIC）
./http_bench -d 30s -c 15 -http http3 "https://127.0.0.1:8443/api/test"

# HTTP/3 POST请求
./http_bench -d 60s -c 25 -http http3 -m POST "https://127.0.0.1:8443/api/upload" \
  -body '{"file":"test.txt","size":1024}'
```

## WebSocket测试

### 1. 基础WebSocket测试

```bash
# WebSocket连接测试
./http_bench -d 30s -c 10 -http ws "ws://127.0.0.1:8080/ws" \
  -body '{"type":"ping","data":"hello"}'
```

### 2. 安全WebSocket测试

```bash
# WSS (WebSocket Secure) 测试
./http_bench -d 60s -c 20 -http wss "wss://127.0.0.1:8443/ws" \
  -body '{"type":"message","content":"secure websocket test"}'
```

## 分布式压测

### 1. 启动Worker节点

```bash
# 在机器1上启动worker
./http_bench -listen "192.168.1.10:12710" -verbose 1

# 在机器2上启动worker
./http_bench -listen "192.168.1.11:12710" -verbose 1

# 在机器3上启动worker
./http_bench -listen "192.168.1.12:12710" -verbose 1
```

### 2. 运行分布式测试

```bash
# 协调多个worker进行压测
./http_bench -d 300s -c 100 "http://target-server.com/api/test" \
  -W "192.168.1.10:12710" \
  -W "192.168.1.11:12710" \
  -W "192.168.1.12:12710" \
  -verbose 1

# 分布式POST测试
./http_bench -d 600s -c 200 -m POST "http://target-server.com/api/load-test" \
  -body '{"test_id":"distributed_test","timestamp":"2024-01-01T00:00:00Z"}' \
  -W "192.168.1.10:12710" \
  -W "192.168.1.11:12710" \
  -W "192.168.1.12:12710"
```

## 模板函数使用

### 1. 随机数据生成

```bash
# 随机整数
./http_bench -n 100 -c 5 "http://127.0.0.1:8080/api/test?id={{ random 1 10000 }}"

# 随机字符串
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/users" \
  -body '{"username":"{{ randomString 8 }}","email":"{{ randomString 10 }}@test.com"}'

# 随机数字字符串
./http_bench -n 100 -c 5 "http://127.0.0.1:8080/api/order?order_id={{ randomNum 10 }}"
```

### 2. 日期和时间函数

```bash
# 当前日期
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/events" \
  -body '{"event_date":"{{ date "YMD" }}","timestamp":"{{ date "YMDHMS" }}"}'

# 随机日期
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/history?date={{ randomDate "YMD" }}"
```

### 3. UUID和字符串处理

```bash
# UUID生成
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/sessions" \
  -body '{"session_id":"{{ UUID }}","user_id":{{ random 1 1000 }}}'

# 字符串转义
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/search?q={{ randomString 10 | escape }}"
```

### 4. 十六进制转换

```bash
# 字符串转十六进制
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/encode" \
  -body '{"data":"{{ stringToHex "hello world" }}"}'

# 十六进制转字符串
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/decode" \
  -body '{"hex_data":"{{ hexToString "48656c6c6f20576f726c64" }}"}'
```

### 5. 数学函数

```bash
# 整数求和
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/calc?result={{ intSum 10 20 30 40 }}"

# 组合使用多个函数
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/complex" \
  -body '{"id":"{{ UUID }}","value":{{ random 1 1000 }},"name":"{{ randomString 8 }}","date":"{{ date "YMD" }}"}'
```

## 高级配置

### 1. 认证和代理

```bash
# Basic认证
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/secure" \
  -a "username:password"

# 使用HTTP代理
./http_bench -n 500 -c 10 "http://target-server.com/api/test" \
  -x "proxy-server.com:8080"

# 组合使用认证和代理
./http_bench -n 1000 -c 15 "http://target-server.com/api/secure" \
  -a "admin:secret123" \
  -x "proxy-server.com:8080"
```

### 2. 连接控制

```bash
# 禁用压缩
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" \
  --disable-compression

# 禁用Keep-Alive
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" \
  --disable-keepalive

# 设置超时时间（毫秒）
./http_bench -n 1000 -c 20 -t 5000 "http://127.0.0.1:8080/api/slow"
```

### 3. 输出格式

```bash
# CSV格式输出
./http_bench -n 1000 -c 20 -o csv "http://127.0.0.1:8080/api/test" > results.csv

# 详细日志输出
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 0  # TRACE
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 1  # DEBUG
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 2  # INFO
```

### 4. Web仪表盘

```bash
# 启动Web仪表盘
./http_bench -dashboard "127.0.0.1:12345" -verbose 1

# 然后在浏览器中访问: http://127.0.0.1:12345
```

## 性能调优

### 1. CPU核心数设置

```bash
# 使用所有CPU核心
./http_bench -n 10000 -c 100 "http://127.0.0.1:8080/api/test" --cpus 8

# 限制使用4个CPU核心
./http_bench -n 10000 -c 100 "http://127.0.0.1:8080/api/test" --cpus 4
```

### 2. 环境变量优化

```bash
# 设置GC百分比以减少垃圾回收频率
export HTTPBENCH_GOGC=200
./http_bench -n 50000 -c 500 "http://127.0.0.1:8080/api/test"

# 设置Worker API端点
export HTTPBENCH_WORKERAPI="/api/v2/worker"
./http_bench -dashboard "127.0.0.1:12345"
```

### 3. 大规模压测示例

```bash
# 高并发短时间测试
./http_bench -d 60s -c 1000 -q 5000 "http://127.0.0.1:8080/api/test" --cpus 16

# 长时间稳定性测试
./http_bench -d 24h -c 200 -q 1000 "http://127.0.0.1:8080/api/test" --cpus 8

# 极限压测
./http_bench -d 300s -c 2000 "http://127.0.0.1:8080/api/test" \
  --disable-keepalive \
  --cpus 32 \
  -verbose 1
```

## 故障排除

### 1. 常见错误处理

```bash
# 连接超时问题 - 增加超时时间
./http_bench -n 1000 -c 20 -t 10000 "http://slow-server.com/api/test"

# 证书问题 - 使用HTTP而非HTTPS进行测试
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test"

# 内存不足 - 减少并发数
./http_bench -n 10000 -c 50 "http://127.0.0.1:8080/api/test"
```

### 2. macOS编译问题

```bash
# 如果在macOS Catalina上编译遇到问题
export CGO_CPPFLAGS="-Wno-error -Wno-nullability-completeness -Wno-expansion-to-defined"
go build .
```

### 3. 调试技巧

```bash
# 启用详细日志进行调试
./http_bench -n 10 -c 2 "http://127.0.0.1:8080/api/test" -verbose 0

# 测试单个请求
./http_bench -n 1 -c 1 "http://127.0.0.1:8080/api/test" -verbose 0

# 检查连接问题
./http_bench -n 5 -c 1 -t 30000 "http://127.0.0.1:8080/api/test" -verbose 1
```

## 实际场景示例

### 1. API性能基准测试

```bash
# 用户登录API测试
./http_bench -d 300s -c 50 -m POST "http://api.example.com/auth/login" \
  -H "Content-Type: application/json" \
  -body '{"username":"{{ randomString 8 }}","password":"test123"}' \
  -verbose 2

# 数据查询API测试
./http_bench -d 600s -c 100 -q 200 "http://api.example.com/users/{{ random 1 10000 }}" \
  -H "Authorization: Bearer token123" \
  -verbose 2
```

### 2. 电商网站压测

```bash
# 商品列表页面
./http_bench -d 1800s -c 200 "http://shop.example.com/products?page={{ random 1 100 }}" \
  -verbose 2

# 购物车操作
./http_bench -d 900s -c 80 -m POST "http://shop.example.com/cart/add" \
  -H "Content-Type: application/json" \
  -body '{"product_id":{{ random 1 1000 }},"quantity":{{ random 1 5 }},"user_id":"{{ UUID }}"}' \
  -verbose 2
```

### 3. 微服务架构测试

```bash
# 服务间通信测试
./http_bench -d 600s -c 150 -m POST "http://service-a.internal:8080/api/process" \
  -H "X-Request-ID: {{ UUID }}" \
  -H "Content-Type: application/json" \
  -body '{"data":"{{ randomString 50 }}","timestamp":"{{ date "YMDHMS" }}"}' \
  -verbose 1
```

### 4. 负载均衡测试

```bash
# 测试负载均衡器分发
./http_bench -d 1200s -c 300 "http://lb.example.com/health" \
  -H "X-Client-ID: {{ randomString 16 }}" \
  -verbose 2
```