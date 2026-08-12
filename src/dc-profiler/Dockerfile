# Data Collection Profiler — multi-stage build: Vite/React frontend + Go backend.

# --- Stage 1: build the React frontend into static assets ---
FROM node:20-bookworm-slim AS frontend-build
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# --- Stage 2: build the Go backend binary ---
FROM golang:1.23-bookworm AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -o /out/server .

# --- Stage 3: runtime image ---
# Debian base so the Workbench post-startup script (which uses apt-get + sudo to
# install the wb CLI, gcsfuse, etc.) works as designed.
FROM debian:bookworm-slim
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl sudo && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=backend-build /out/server /app/server
COPY --from=frontend-build /build/dist /app/static
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh

ENV STATIC_DIR=/app/static
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD curl -f http://localhost:8080/api/health || exit 1
CMD ["./start.sh"]
