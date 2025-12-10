# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .
RUN go build -o http_bench .

# Final stage
FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/http_bench .

EXPOSE 9000
ENTRYPOINT ["./http_bench", "-listen", "0.0.0.0:9000"]