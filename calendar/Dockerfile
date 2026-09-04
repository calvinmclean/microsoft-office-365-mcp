FROM golang:1.25.0-alpine AS builder

WORKDIR /src/calendar

COPY calendar/go.mod calendar/go.sum ./
COPY metrics /src/metrics
RUN go mod download

COPY calendar/. .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /calendar-mcp-go .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /calendar-mcp-go .

EXPOSE 9000

CMD ["./calendar-mcp-go"]
