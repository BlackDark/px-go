# Docker & Kubernetes

Run px-go as a **central outbound proxy** when many containers must reach the internet through a corporate upstream.

Examples below assume the **upstream proxy requires no authentication**. For NTLM, Negotiate, or password-based upstream auth, see [Authentication](authentication.md).

Back to [deployment overview](deployment.md).

## Recommended `px.ini` (shared proxy)

Uses [network defaults from the deployment guide](deployment.md#recommended-network-defaults):

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE

gateway = 1
allow = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
noproxy = .svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16

port = 3128

[client]
client_auth = NONE

[settings]
threads = 128
idle = 300
socktimeout = 20
proxyreload = 300
foreground = 1
log = 4
log_level = INFO
```

> **Upstream auth required?** Add `username`, `PX_PASSWORD`, and `auth=NTLM` — [Authentication → Kubernetes](authentication.md#kubernetes-upstream-credentials).
>
> **Exposed beyond a trusted cluster network?** Enable `client_auth` — [Authentication → client auth](authentication.md#client-authentication).

CLI equivalent:

```bash
px-go \
  --gateway \
  --server=corp-proxy:8080 \
  --auth=NONE \
  --allow='10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
  --noproxy='.svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16' \
  --foreground \
  --log=4
```

## Quick Docker run

```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 \
  px-go --server=corp-proxy:8080 --gateway --foreground --log=4 \
  --allow='10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
  --noproxy='.svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16'
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
    command:
      - --gateway
      - --auth=NONE
      - --allow=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
      - --noproxy=.svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
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
    # deploy.resources applies to Docker Swarm; for plain Compose use mem_limit/cpus if needed
    healthcheck:
      test: ["CMD", "/px-go", "--health-check", "--port=3128"]
      interval: 30s
      timeout: 5s
      retries: 3

  app:
    depends_on:
      - px
    environment:
      HTTP_PROXY: http://px:3128
      HTTPS_PROXY: http://px:3128
      NO_PROXY: localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

## Kubernetes

Replace `10.0.0.0/8` in `allow` with your **pod CIDR** if it differs (e.g. `10.244.0.0/16`).

```yaml
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
          args:
            - --gateway
            - --auth=NONE
            - --allow=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
            - --noproxy=.svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16
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
    value: http://px-go:3128
  - name: HTTPS_PROXY
    value: http://px-go:3128
  - name: NO_PROXY
    value: .svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16
```

Add **NetworkPolicy** so only intended namespaces reach port 3128. For upstream or client credentials, see [Authentication](authentication.md).

## Deployment patterns

| Pattern | When to use |
|---|---|
| **Shared Deployment/Service** | Many pods, one px instance, easier ops |
| **Sidecar per pod** | Pod-specific upstream config or isolation — add px container to pod spec, set `HTTP_PROXY=http://127.0.0.1:3128` in app container |
| **DaemonSet (hostNetwork)** | Node-level proxy; run px on each node with `hostonly=1`, apps use node IP — see [VM & bare metal](vm-bare-metal.md#hostonly--docker--containers-on-the-same-host) |

For VM/bare-metal equivalents, see [VM & bare metal](vm-bare-metal.md).

## Troubleshooting

| Symptom | Check |
|---|---|
| 403 from px | Client IP not in `allow`; tighten or fix pod CIDR |
| 407 from upstream | [Authentication](authentication.md) — upstream requires creds not in example |
| Readiness probe fails | px not listening; distroless needs `exec` probe with `/px-go --health-check` |
| Internal traffic via corporate proxy | Expand `NO_PROXY` / `--noproxy` with `.svc`, pod CIDR |
| Open relay concern | Never publish `--gateway` without `--allow`; see [security](security.md) |

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

- [Authentication](authentication.md)
- [Security considerations](security.md)
- [VM & bare metal](vm-bare-metal.md)
