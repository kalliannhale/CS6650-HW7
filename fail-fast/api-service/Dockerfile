FROM golang:1.21-alpine
WORKDIR /app
COPY . .
RUN go mod init api-service || true
RUN go get github.com/sony/gobreaker@v0.5.0
RUN go mod tidy
RUN go build -o main .
CMD ["./main"]