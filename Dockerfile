FROM golang:1.21-alpine
COPY ./ /app 
RUN chmod +x -R *
WORKDIR /app
ENTRYPOINT ["./http_bench", "-listen", "0.0.0.0:9000"]