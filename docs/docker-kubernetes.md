# Docker & Kubernetes

Run px-go as a **central outbound proxy** when many containers must reach the internet through a corporate upstream (NTLM/Negotiate/Basic).

Linux containers **cannot use Windows SSPI** — provide explicit upstream credentials or Kerberos keytabs. See [security considerations](security.md).

Back to [deployment overview](deployment.md).

## Recommended `px.ini` (shared proxy)

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\service-account
; password via PX_PASSWORD env / K8s Secret — never commit

gateway = 1
allow = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
noproxy = .svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1

auth = NTLM
port = 3128

[client]
client_auth = BASIC
client_username = proxyuser
; client password via PX_CLIENT_PASSWORD env / Secret

[settings]
threads = 128
idle = 300
socktimeout = 20
proxyreload = 300
foreground = 1
log = 4
log_level = INFO
```

CLI equivalent:

```bash
px-go \
  --gateway \
  --server=corp-proxy:8080 \
  --username='DOMAIN\user' \
  --auth=NTLM \
  --allow='10.0.0.0/8,172.16.0.0/12' \
  --noproxy='.svc,.cluster.local,10.0.0.0/8' \
  --client-auth=BASIC \
  --client-username=proxyuser \
  --foreground \
  --log=4
```

## Quick Docker run

```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 px-go --gateway --foreground --log=4
```

Released images: `ghcr.io/blackdark/px-go:latest` (multi-arch).

## Docker Compose

```yaml
services:
  px:
    image: ghcr.io/blackdark/px-go:latest
    ports:
      - "3128:3128"
    environment:
      PX_SERVER: corp-proxy.example.com:8080
      PX_USERNAME: "DOMAIN\\service-account"
      PX_PASSWORD: ${PX_PASSWORD}
      PX_CLIENT_AUTH: BASIC
      PX_CLIENT_USERNAME: proxyuser
      PX_CLIENT_PASSWORD: ${PX_CLIENT_PASSWORD}
    command:
      - --gateway
      - --allow=172.16.0.0/12,192.168.0.0/16
      - --noproxy=.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12
      - --foreground
      - --log=4
    deploy:
      resources:
        limits:
          memory: 512Mi
          cpu: "1"
        requests:
          memory: 128Mi
          cpu: 100m
    healthcheck:
      test: ["CMD", "/px-go", "--health-check", "--port=3128"]
      interval: 30s
      timeout: 5s
      retries: 3
```

Point other services at `http://px:3128` via `HTTP_PROXY` / `HTTPS_PROXY` and set `NO_PROXY` for internal hosts.

## Kubernetes

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: px-credentials
stringData:
  PX_PASSWORD: "change-me"
  PX_CLIENT_PASSWORD: "change-me"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: px-go
spec:
  replicas: 2
  selector:
    matchLabels:
      app: px-go
  template:
    metadata:
      labels:
        app: px-go
    spec:
      containers:
        - name: px-go
          image: ghcr.io/blackdark/px-go:latest
          ports:
            - containerPort: 3128
              name: proxy
          env:
            - name: PX_SERVER
              value: corp-proxy.example.com:8080
            - name: PX_USERNAME
              value: "DOMAIN\\service-account"
            - name: PX_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: px-credentials
                  key: PX_PASSWORD
            - name: PX_CLIENT_AUTH
              value: BASIC
            - name: PX_CLIENT_USERNAME
              value: proxyuser
            - name: PX_CLIENT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: px-credentials
                  key: PX_CLIENT_PASSWORD
          args:
            - --gateway
            - --allow=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
            - --noproxy=.svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1
            - --foreground
            - --log=4
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: "1"
              memory: 512Mi
          livenessProbe:
            exec:
              command: ["/px-go", "--health-check", "--port=3128"]
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            exec:
              command: ["/px-go", "--health-check", "--port=3128"]
            periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: px-go
spec:
  selector:
    app: px-go
  ports:
    - port: 3128
      targetPort: proxy
```

Wire client pods:

```yaml
env:
  - name: HTTP_PROXY
    value: http://proxyuser:$(PX_CLIENT_PASSWORD)@px-go:3128
  - name: HTTPS_PROXY
    value: http://proxyuser:$(PX_CLIENT_PASSWORD)@px-go:3128
  - name: NO_PROXY
    value: .svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1
```

Add **NetworkPolicy** so only intended namespaces can reach port 3128.

## Deployment patterns

| Pattern | When to use |
|---|---|
| **Shared Deployment/Service** | Many pods, one credential set, easier ops |
| **Sidecar per pod** | Pod-specific upstream auth or isolation |
| **DaemonSet (hostNetwork)** | Node-level proxy for non-K8s workloads on the same host; pair with `hostonly=1` on the node |

For VM/bare-metal equivalents of sidecar and per-host proxy, see [VM & bare metal](vm-bare-metal.md).

## Sizing

| Load | Replicas | CPU (limit) | Memory (limit) | `threads` |
|---|---|---|---|---|
| Dev / CI (~10 concurrent setups) | 1 | 250m | 128Mi | 64 |
| Typical cluster (~50 pods, bursty) | 2 | 500m–1 | 256–512Mi | 128 |
| Heavy AI / long-lived tunnels | 2–3 | 1–2 | 512Mi–1Gi | 256 |

Notes:

- `threads` limits **connection setup** (dial + upstream auth), not active CONNECT tunnels.
- Raise `idle` (300+) for agents that keep long-lived connections open between bursts.
- Watch **file descriptors** under heavy tunnel load.
- Distroless image has no shell — use `exec` probes and env/CLI config only.

## See also

- [Security considerations](security.md)
- [VM & bare metal](vm-bare-metal.md)
