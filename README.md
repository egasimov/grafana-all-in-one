# Go OpenTelemetry Demo

This project demonstrates a complete observability setup using:
- Go application with OpenTelemetry SDK
- OpenTelemetry Collector
- Grafana
- Mimir (metrics)
- Tempo (traces)
- Loki (logs)

## Getting Started

1. Start all services:
```bash
docker compose up --build
```

2. Access services:
- Grafana: http://localhost:3000
- Go App: http://localhost:8080

3. Generate some traffic:
```bash
while true; do curl http://localhost:8080/; sleep 1; done
```

## Observability Data

- **Metrics**: View in Grafana using the Mimir datasource
  - `http_requests_total`: Total number of HTTP requests
  - `http_request_duration`: HTTP request duration histogram

- **Traces**: View in Grafana using the Tempo datasource
  - Each HTTP request creates a trace
  - Includes attributes like path and method

- **Logs**: View in Grafana using the Loki datasource
  - Application logs are forwarded through OpenTelemetry Collector
