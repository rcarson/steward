# stack-agent monitoring example

Full Docker Compose stack: **stack-agent** + **Prometheus** + **Grafana**, wired together on a shared bridge network with named volumes.

## Prerequisites

- Docker Engine 24+
- Docker Compose plugin (`docker compose`)

## Quick start

1. Copy this directory to your host:

   ```sh
   cp -r examples/monitoring /opt/stack-agent-monitoring
   cd /opt/stack-agent-monitoring
   ```

2. Add a `config.yaml` for stack-agent (this file is not included):

   ```sh
   cp ../../config.example.yaml config.yaml
   # edit config.yaml to suit your environment
   ```

3. Optionally set your API token in a `.env` file instead of editing `compose.yaml`:

   ```sh
   echo "STACK_AGENT_DEFAULT_TOKEN=your-token-here" > .env
   # then uncomment the token line in compose.yaml
   ```

4. Start the stack:

   ```sh
   docker compose up -d
   ```

5. Open Grafana at <http://localhost:3000> and log in with **admin / admin**.
   The `stack-agent` dashboard is pre-provisioned under *Dashboards*.

## Services

| Service | Port | Description |
|---|---|---|
| stack-agent | 2112 | Polls Docker stacks and exposes `/metrics` and `/healthz` |
| Prometheus | 9090 | Scrapes stack-agent every 15 s; 30-day retention |
| Grafana | 3000 | Pre-provisioned with Prometheus datasource and stack-agent dashboard |

## Dashboard panels

| Panel | Metric |
|---|---|
| Poll rate | `rate(stackagent_polls_total[5m])` by stack / result |
| Deploy rate | `rate(stackagent_deploys_total[5m])` by stack |
| Deploy duration p50 | `histogram_quantile(0.50, ...)` by stack |
| Deploy duration p95 | `histogram_quantile(0.95, ...)` by stack |
| Last deploy | Seconds since last deploy per stack |

## Customisation

**Change the Grafana admin password** — set `GF_SECURITY_ADMIN_PASSWORD` in `compose.yaml` (or a `.env` file) before first boot.

**Adjust Prometheus retention** — edit the `--storage.tsdb.retention.time` flag in the `prometheus` service command.

**Use a bind mount for Prometheus data** — replace the `prometheus-data` named volume with a host path:

```yaml
volumes:
  - /data/prometheus:/prometheus
```

**Expose stack-agent metrics only internally** — remove the `ports` block from the `stack-agent` service; Prometheus reaches it via the `monitoring` network without a published port.
