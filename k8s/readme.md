# k8s

Two-tier OTel collector setup. Apps send spans -> `otel-lb` -> `otel-tail-sampler` -> VictoriaTraces.

| File                     | Purpose                                                                                                                                                                          |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `otel-lb.yaml`           | Helm values for the load-balancer collector. Hashes by traceID and forwards to the correct tail-sampler pod. Runs as DaemonSet by default (see topology comment at top of file). |
| `otel-tail-sampler.yaml` | Helm values for the tail-sampling collector (StatefulSet, headless). Keeps errors, slow traces (>1s), and 1% baseline. Exports to VictoriaTraces.                                |
| `test-deployment.yaml`   | Deployment + Service for the test Go service.                                                                                                                                    |
| `load-test.sh`           | Local load test script. Fires a shuffled mix of `/success`, `/failure`, `/latency`, `/heavy`. All counts and concurrency tunable via env vars.                                   |
| `load-test-job.yaml`     | Same script as a Kubernetes Job + ConfigMap for in-cluster load testing.                                                                                                         |
| `grafana-values.yaml`    | Helm values for Grafana to visualise traces.                                                                                                                                     |

<img width="2864" height="1524" alt="image" src="https://github.com/user-attachments/assets/39454a80-19ac-448b-bf04-b0f638352f14" />
<img width="3168" height="1364" alt="image" src="https://github.com/user-attachments/assets/4577c1e1-6c83-408b-8f26-c704ef97d0d8" />

## Topology

**DaemonSet (default):** apps send to `$(NODE_IP):4317`: one local hop, no cross-node traffic before the lb.  
**Deployment:** apps send to `otel-lb.observability.svc.cluster.local:4317`: simpler, two replicas for redundancy.

Switch by changing `mode` in `otel-lb.yaml` and the `OTEL_EXPORTER_OTLP_ENDPOINT` env var in `test-deployment.yaml` (both files have comments explaining the exact lines to change).

## Install

```bash
helm upgrade --install otel-lb opentelemetry-helm/opentelemetry-collector \
  --namespace observability --create-namespace -f otel-lb.yaml

helm upgrade --install otel-tail-sampler opentelemetry-helm/opentelemetry-collector \
  --namespace observability -f otel-tail-sampler.yaml
```

## Load test

```bash
# local
./load-test.sh
SUCCESS_N=5000 HEAVY_N=500 FAILURE_N=50 ./load-test.sh

# in-cluster
kubectl apply -f load-test-job.yaml
kubectl logs -f job/load-test
```
