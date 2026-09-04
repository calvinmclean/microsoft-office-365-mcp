FROM golang:1.27.1-alpine AS builder

WORKDIR /src/contact

COPY contact/go.mod contact/go.sum ./
COPY metrics /src/metrics
RUN go mod download

COPY contact/. .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /contact-mcp-go .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /contact-mcp-go .

EXPOSE 9000

CMD ["./contact-mcp-go"]
