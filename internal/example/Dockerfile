FROM golang:latest AS builder
WORKDIR /work
COPY . .
RUN go generate && cd internal/example && CGO_ENABLED=0 go build -o /app .

FROM scratch
COPY --from=builder /app /app
ENTRYPOINT ["/app"]
