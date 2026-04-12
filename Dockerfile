# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy source and dependency listing
COPY go.mod main.go ./

# Download all dependencies dynamically
RUN go mod tidy

# Build the executable
RUN CGO_ENABLED=0 go build -o server main.go

# Run stage
FROM alpine:latest

# Install su-exec for natively dropping privileges during boot mapping
RUN apk add --no-cache su-exec

WORKDIR /app

# Copy the generated binary and config seamlessly
COPY --from=builder /app/server .
COPY config.yaml .

# Provide the dynamic execution entrypoint
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose port and launch via the entry wrapper
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./server"]
