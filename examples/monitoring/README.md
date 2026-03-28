# stack-agent monitoring

Grafana dashboard and Prometheus scrape config for stack-agent.

## Files

| File | Description |
|---|---|
| `stack-agent-dashboard.json` | Pre-built Grafana dashboard — import via the Grafana UI |
| `prometheus-scrape.yaml` | Scrape job snippet — add to your `scrape_configs` |

## Grafana dashboard

Import `stack-agent-dashboard.json` via **Dashboards → Import** in the Grafana UI, or drop it into your provisioning directory. Select your Prometheus datasource when prompted.

### Panels

| Panel | Metric |
|---|---|
| Poll rate | `rate(stackagent_polls_total[5m])` by stack / result |
| Deploy rate | `rate(stackagent_deploys_total[5m])` by stack |
| Deploy duration p50 | `histogram_quantile(0.50, rate(stackagent_deploy_duration_seconds_bucket[10m]))` by stack |
| Deploy duration p95 | `histogram_quantile(0.95, rate(stackagent_deploy_duration_seconds_bucket[10m]))` by stack |
| Last deploy | Seconds since last successful deploy per stack |

## Prometheus scrape config

Add the contents of `prometheus-scrape.yaml` to the `scrape_configs` section of your `prometheus.yaml`, adjusting the target address to match your stack-agent host:

```yaml
scrape_configs:
  - job_name: stack-agent
    static_configs:
      - targets:
          - <stack-agent-host>:2112
```

stack-agent exposes metrics at `:2112/metrics` by default. Override with the `STACK_AGENT_HTTP_ADDR` environment variable.
