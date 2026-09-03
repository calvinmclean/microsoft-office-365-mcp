FROM golang:1.25.0-alpine AS builder

WORKDIR /src/onedrive

COPY onedrive/go.mod onedrive/go.sum ./
COPY metrics /src/metrics
RUN go mod download

COPY onedrive/. .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /onedrive-mcp-go .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /onedrive-mcp-go .

EXPOSE 9000

CMD ["./onedrive-mcp-go"]
