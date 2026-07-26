FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mcp-shield ./cmd/mcp-shield
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mcp-shield-testserver ./cmd/mcp-shield-testserver

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=builder /out/mcp-shield ./bin/mcp-shield
COPY --from=builder /out/mcp-shield-testserver ./bin/mcp-shield-testserver
COPY web ./web
EXPOSE 8080 8081
ENTRYPOINT ["./bin/mcp-shield"]
