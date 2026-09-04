FROM golang:1.27.1-alpine AS builder

WORKDIR /src/excel

COPY excel/go.mod excel/go.sum ./
COPY metrics /src/metrics
RUN go mod download

COPY excel/. .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /excel-mcp-go .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /excel-mcp-go .

EXPOSE 9000

CMD ["./excel-mcp-go"]
