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
WORKDIR /app

# Copy the generated binary and default config
COPY --from=builder /app/server .
COPY config.yaml .

# Expose port and run the binary
EXPOSE 8080
CMD ["./server"]
