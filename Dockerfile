FROM golang:1.20
COPY ./ /app 
RUN chmod +x -R *
WORKDIR /app
ENTRYPOINT ["./http_bench", "-dashboard", "127.0.0.1:12345"]