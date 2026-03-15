FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o 86-challenge .

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/86-challenge .
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

EXPOSE 8086
CMD ["./86-challenge"]
