# Dhunter — single image: Go server (API + SPA) + Python agent sidecar.
# Multi-stage: build Go binaries, build the Vue SPA, then assemble.

# ---- Stage 1: Go server + MCP binaries --------------------------------
FROM golang:1.22-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/dhunter-server ./cmd/dhunter-server
RUN CGO_ENABLED=0 go build -o /out/dhunter-mcp ./cmd/dhunter-mcp

# ---- Stage 2: Vue frontend --------------------------------------------
FROM node:20-alpine AS fe-build
WORKDIR /fe
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- Stage 3: final runtime --------------------------------------------
FROM python:3.12-slim
WORKDIR /app

COPY --from=go-build /out/ /app/bin/
COPY --from=fe-build /fe/dist /app/frontend/dist

COPY agents/requirements.txt /app/agents/requirements.txt
RUN pip install --no-cache-dir -r /app/agents/requirements.txt
COPY agents/ /app/agents/
COPY configs/dhunter.yaml /app/configs/dhunter.yaml
COPY scripts/start-container.sh /app/scripts/start-container.sh
RUN chmod +x /app/scripts/start-container.sh /app/bin/dhunter-server /app/bin/dhunter-mcp

ENV DHUNTER_AGENT_PORT=9100 \
    DHUNTER_MCP_PORT=9124 \
    DHUNTER_SERVER_PORT=13343

EXPOSE 13343
CMD ["/app/scripts/start-container.sh"]
