FROM node:24-trixie-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-trixie AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/crosstown ./cmd/crosstown

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends postgresql ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -u 1000 app
WORKDIR /app
COPY --from=server /out/crosstown ./crosstown
COPY --from=web /web/dist ./web/dist
COPY config.toml ./config.toml
COPY data/graph.bin.gz data/demand.csv.gz data/places.json.gz data/zones.json data/calibration.json ./data/
COPY docker/start.sh ./start.sh
RUN chown -R app:app /app
USER app
ENV PORT=7860 EMBEDDED_POSTGRES=1
EXPOSE 7860
CMD ["sh", "/app/start.sh"]
