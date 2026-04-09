FROM golang:1.22 AS builder
WORKDIR /app
COPY go.mod .
COPY *.go .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /app/app

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/app /app/app
ENTRYPOINT ["/app/app"]
