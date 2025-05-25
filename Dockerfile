FROM golang:1.20
COPY ./ /app 
RUN chmod +x -R *
WORKDIR /app
ENTRYPOINT ["./http_bench", "--dashboard", "0.0.0.0:9000"]