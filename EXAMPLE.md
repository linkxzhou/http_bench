# HTTP Bench Load Testing Examples

This document provides detailed usage examples for the HTTP Bench tool, covering various testing scenarios and advanced features.

## Table of Contents

1. [Basic Load Testing Examples](#basic-load-testing-examples)
2. [HTTP Protocol Testing](#http-protocol-testing)
3. [WebSocket Testing](#websocket-testing)
4. [Distributed Load Testing](#distributed-load-testing)
5. [Template Functions Usage](#template-functions-usage)
6. [Advanced Configuration](#advanced-configuration)
7. [Performance Tuning](#performance-tuning)
8. [Troubleshooting](#troubleshooting)

## Basic Load Testing Examples

### 1. Simple GET Requests

```bash
# Send 1000 requests with 10 concurrent connections
./http_bench -n 1000 -c 10 "http://127.0.0.1:8080/api/test"

# Specify request method
./http_bench -n 1000 -c 10 -m GET "http://127.0.0.1:8080/api/users"
```

### 2. Duration-based Testing

```bash
# Load test for 30 seconds
./http_bench -d 30s -c 50 "http://127.0.0.1:8080/api/test"

# Load test for 5 minutes
./http_bench -d 5m -c 100 "http://127.0.0.1:8080/api/test"

# Load test for 1 hour
./http_bench -d 1h -c 200 "http://127.0.0.1:8080/api/test"
```

### 3. QPS Rate Limiting

```bash
# Limit to 100 requests per second
./http_bench -d 60s -c 10 -q 100 "http://127.0.0.1:8080/api/test"

# Limit to 500 requests per second for 10 minutes
./http_bench -d 10m -c 50 -q 500 "http://127.0.0.1:8080/api/test"
```

### 4. POST Request Examples

```bash
# Simple POST request
./http_bench -n 1000 -c 20 -m POST "http://127.0.0.1:8080/api/users" \
  -body '{"name":"test","email":"test@example.com"}'

# POST request with custom headers
./http_bench -n 500 -c 10 -m POST "http://127.0.0.1:8080/api/login" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer token123" \
  -body '{"username":"admin","password":"secret"}'
```

### 5. Testing with .http File (Multiple Requests)

Support standard `.http` file format (IntelliJ/VSCode REST Client compatible). You can define multiple requests separated by `###`.
The benchmark will run sequentially for each request defined in the file.

```bash
# Create a .http file with multiple requests
cat > requests.http << EOF
# Request 1: Get Users
GET http://127.0.0.1:8080/api/users

###

# Request 2: Create Data with JSON body
POST http://127.0.0.1:8080/api/data
Content-Type: application/json

{
    "name": "test",
    "value": 123
}
EOF

# Run benchmark using requests from file
# This will run the benchmark for the first request, then the second.
./http_bench -n 1000 -c 20 -file requests.http
```

## HTTP Protocol Testing

### 1. HTTP/1.1 Testing (Default)

```bash
./http_bench -d 60s -c 50 -http http1 "http://127.0.0.1:8080/api/test"
```

### 2. HTTP/2 Testing

```bash
# HTTP/2 GET request
./http_bench -d 30s -c 20 -http http2 "https://127.0.0.1:8443/api/test"

# HTTP/2 POST request
./http_bench -d 60s -c 30 -http http2 -m POST "https://127.0.0.1:8443/api/data" \
  -body '{"message":"HTTP/2 test"}'
```

### 3. HTTP/3 Testing

```bash
# HTTP/3 testing (requires server QUIC support)
./http_bench -d 30s -c 15 -http http3 "https://127.0.0.1:8443/api/test"

# HTTP/3 POST request
./http_bench -d 60s -c 25 -http http3 -m POST "https://127.0.0.1:8443/api/upload" \
  -body '{"file":"test.txt","size":1024}'
```

## WebSocket Testing

### 1. Basic WebSocket Testing

```bash
# WebSocket connection testing
./http_bench -d 30s -c 10 -http ws "ws://127.0.0.1:8080/ws" \
  -body '{"type":"ping","data":"hello"}'
```

### 2. Secure WebSocket Testing

```bash
# WSS (WebSocket Secure) testing
./http_bench -d 60s -c 20 -http wss "wss://127.0.0.1:8443/ws" \
  -body '{"type":"message","content":"secure websocket test"}'
```

## Distributed Load Testing

### 1. Starting Worker Nodes

```bash
# Start worker on machine 1
./http_bench -listen "192.168.1.10:12710" -verbose 1

# Start worker on machine 2
./http_bench -listen "192.168.1.11:12710" -verbose 1

# Start worker on machine 3
./http_bench -listen "192.168.1.12:12710" -verbose 1
```

### 2. Running Distributed Tests

```bash
# Coordinate multiple workers for load testing
./http_bench -d 300s -c 100 "http://target-server.com/api/test" \
  -W "192.168.1.10:12710" \
  -W "192.168.1.11:12710" \
  -W "192.168.1.12:12710" \
  -verbose 1

# Distributed POST testing
./http_bench -d 600s -c 200 -m POST "http://target-server.com/api/load-test" \
  -body '{"test_id":"distributed_test","timestamp":"2024-01-01T00:00:00Z"}' \
  -W "192.168.1.10:12710" \
  -W "192.168.1.11:12710" \
  -W "192.168.1.12:12710"
```

## Template Functions Usage

### 1. Random Data Generation

```bash
# Random integers
./http_bench -n 100 -c 5 "http://127.0.0.1:8080/api/test?id={{ random 1 10000 }}"

# Random strings
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/users" \
  -body '{"username":"{{ randomString 8 }}","email":"{{ randomString 10 }}@test.com"}'

# Random numeric strings
./http_bench -n 100 -c 5 "http://127.0.0.1:8080/api/order?order_id={{ randomNum 10 }}"
```

### 2. Date and Time Functions

```bash
# Current date
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/events" \
  -body '{"event_date":"{{ date \"YMD\" }}","timestamp":"{{ date \"YMDHMS\" }}"}'

# Random date
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/history?date={{ randomDate \"YMD\" }}"
```

### 3. UUID and String Processing

```bash
# UUID generation
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/sessions" \
  -body '{"session_id":"{{ UUID }}","user_id":{{ random 1 1000 }}}'

# String escaping
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/search?q={{ randomString 10 | escape }}"
```

### 4. Hexadecimal Conversion

```bash
# String to hexadecimal
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/encode" \
  -body '{"data":"{{ stringToHex \"hello world\" }}"}'

# Hexadecimal to string
./http_bench -n 50 -c 3 -m POST "http://127.0.0.1:8080/api/decode" \
  -body '{"hex_data":"{{ hexToString \"48656c6c6f20576f726c64\" }}"}'
```

### 5. Mathematical Functions

```bash
# Integer sum
./http_bench -n 50 -c 3 "http://127.0.0.1:8080/api/calc?result={{ intSum 10 20 30 40 }}"

# Combined use of multiple functions
./http_bench -n 100 -c 5 -m POST "http://127.0.0.1:8080/api/complex" \
  -body '{"id":"{{ UUID }}","value":{{ random 1 1000 }},"name":"{{ randomString 8 }}","date":"{{ date \"YMD\" }}"}'
```

## Advanced Configuration

### 1. Authentication and Proxy

```bash
# Basic authentication
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/secure" \
  -a "username:password"

# Using HTTP proxy
./http_bench -n 500 -c 10 "http://target-server.com/api/test" \
  -x "proxy-server.com:8080"

# Combined authentication and proxy
./http_bench -n 1000 -c 15 "http://target-server.com/api/secure" \
  -a "admin:secret123" \
  -x "proxy-server.com:8080"
```

### 2. Connection Control

```bash
# Disable compression
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" \
  --disable-compression

# Disable Keep-Alive
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" \
  --disable-keepalive

# Set timeout (milliseconds)
./http_bench -n 1000 -c 20 -t 5000 "http://127.0.0.1:8080/api/slow"
```

### 3. Output Formats

```bash
# CSV format output
./http_bench -n 1000 -c 20 -o csv "http://127.0.0.1:8080/api/test" > results.csv

# Verbose logging output
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 0  # TRACE
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 1  # DEBUG
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test" -verbose 2  # INFO
```

### 4. Web Dashboard

```bash
# Start Web dashboard
./http_bench -listen "127.0.0.1:12345" -verbose 1

# Then access in browser: http://127.0.0.1:12345
```

## Performance Tuning

### 1. CPU Core Configuration

```bash
# Use all CPU cores
./http_bench -n 10000 -c 100 "http://127.0.0.1:8080/api/test" --cpus 8

# Limit to 4 CPU cores
./http_bench -n 10000 -c 100 "http://127.0.0.1:8080/api/test" --cpus 4
```

### 2. Environment Variable Optimization

```bash
# Set GC percentage to reduce garbage collection frequency
export HTTPBENCH_GOGC=200
./http_bench -n 50000 -c 500 "http://127.0.0.1:8080/api/test"

# Set Worker API endpoint
export HTTPBENCH_WORKERAPI="/v2/worker"
./http_bench -listen "127.0.0.1:12345"
```

### 3. Large-scale Load Testing Examples

```bash
# High concurrency short-term testing
./http_bench -d 60s -c 1000 -q 5000 "http://127.0.0.1:8080/api/test" --cpus 16

# Long-term stability testing
./http_bench -d 24h -c 200 -q 1000 "http://127.0.0.1:8080/api/test" --cpus 8

# Extreme load testing
./http_bench -d 300s -c 2000 "http://127.0.0.1:8080/api/test" \
  --disable-keepalive \
  --cpus 32 \
  -verbose 1
```

## Troubleshooting

### 1. Common Error Handling

```bash
# Connection timeout issues - increase timeout
./http_bench -n 1000 -c 20 -t 10000 "http://slow-server.com/api/test"

# Certificate issues - use HTTP instead of HTTPS for testing
./http_bench -n 1000 -c 20 "http://127.0.0.1:8080/api/test"

# Memory shortage - reduce concurrency
./http_bench -n 10000 -c 50 "http://127.0.0.1:8080/api/test"
```

### 2. macOS Compilation Issues

```bash
# If encountering compilation issues on macOS Catalina
export CGO_CPPFLAGS="-Wno-error -Wno-nullability-completeness -Wno-expansion-to-defined"
go build .
```

### 3. Debugging Tips

```bash
# Enable verbose logging for debugging
./http_bench -n 10 -c 2 "http://127.0.0.1:8080/api/test" -verbose 0

# Test single request
./http_bench -n 1 -c 1 "http://127.0.0.1:8080/api/test" -verbose 0

# Check connection issues
./http_bench -n 5 -c 1 -t 30000 "http://127.0.0.1:8080/api/test" -verbose 1
```

## Real-world Scenario Examples

### 1. API Performance Benchmarking

```bash
# User login API testing
./http_bench -d 300s -c 50 -m POST "http://api.example.com/auth/login" \
  -H "Content-Type: application/json" \
  -body '{"username":"{{ randomString 8 }}","password":"test123"}' \
  -verbose 2

# Data query API testing
./http_bench -d 600s -c 100 -q 200 "http://api.example.com/users/{{ random 1 10000 }}" \
  -H "Authorization: Bearer token123" \
  -verbose 2
```

### 2. E-commerce Website Load Testing

```bash
# Product listing page
./http_bench -d 1800s -c 200 "http://shop.example.com/products?page={{ random 1 100 }}" \
  -verbose 2

# Shopping cart operations
./http_bench -d 900s -c 80 -m POST "http://shop.example.com/cart/add" \
  -H "Content-Type: application/json" \
  -body '{"product_id":{{ random 1 1000 }},"quantity":{{ random 1 5 }},"user_id":"{{ UUID }}"}' \
  -verbose 2
```

### 3. Microservices Architecture Testing

```bash
# Inter-service communication testing
./http_bench -d 600s -c 150 -m POST "http://service-a.internal:8080/api/process" \
  -H "X-Request-ID: {{ UUID }}" \
  -H "Content-Type: application/json" \
  -body '{"data":"{{ randomString 50 }}","timestamp":"{{ date \"YMDHMS\" }}"}' \
  -verbose 1
```

### 4. Load Balancer Testing

```bash
# Test load balancer distribution
./http_bench -d 1200s -c 300 "http://lb.example.com/health" \
  -H "X-Client-ID: {{ randomString 16 }}" \
  -verbose 2
```