# ==========================================
# Build Stage
# ==========================================
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy dependency manifests
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code and HTML assets
COPY server.go ./
COPY index.html ./
COPY camera.html ./

# Build the Go application statically
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main server.go

# ==========================================
# Run Stage
# ==========================================
FROM alpine:latest  

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy the compiled binary from the builder
COPY --from=builder /app/main .

# Copy static web pages
COPY --from=builder /app/index.html .
COPY --from=builder /app/camera.html .

# Expose port (default 8080)
EXPOSE 8080

# Production environment variables
ENV PORT=8080
ENV DEFAULT_EMA_ALPHA=0.3

# Run the server
CMD ["./main"]
