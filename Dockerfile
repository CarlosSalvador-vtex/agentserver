# syntax=docker/dockerfile:1.4

# Stage 1: Build admin frontend (native host arch — JS is portable)
FROM --platform=$BUILDPLATFORM node:22-slim AS frontend
RUN npm install -g pnpm
WORKDIR /app/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY docs/api/openapi.yaml /app/docs/api/openapi.yaml
RUN pnpm openapi:gen
RUN pnpm build

# Stage 2: Build Go backend (native host arch; cross-compile to TARGETARCH)
FROM --platform=$BUILDPLATFORM golang:1.26-trixie AS backend
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -tags goolm -o agentserver .

# Stage 3: Runtime image with Docker CLI (claude-code runs in agent containers)
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && chmod a+r /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*
COPY --from=backend /app/agentserver /usr/local/bin/agentserver
EXPOSE 8080
ENTRYPOINT ["agentserver", "serve"]
