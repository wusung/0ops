# cloudflare-tunnel Helm chart

Cloudflare Tunnel connector pool for the `winshare.tw` zone. Implements the
manifests defined in `docs/features/winshare-subdomain-and-tunnel/spec.md`
§ 5.1 and § 9.1.

## What it deploys

- `Namespace cloudflare-tunnel` with PSA labels `enforce=baseline / warn=restricted`
- `Deployment cloudflared` — 3 replicas, podAntiAffinity by hostname, pinned
  `cloudflare/cloudflared:2025.1.0` image with `--no-autoupdate`
- `Secret cloudflared-tunnel-token` — rendered only when `tunnelToken` is set;
  production should layer in an `ExternalSecret`/`SealedSecret` instead of
  passing the literal value through `values.yaml`
- `NetworkPolicy cloudflared-default` — outbound to Cloudflare edge (TCP 7844 +
  TCP 443) and to the `kube-system` namespace (traefik); no ingress

## Hard rules (enforced)

- `replicaCount >= 3` — chart aborts via `fail` if reduced (spec § 16 #3)
- image tag pinned + `--no-autoupdate` (spec § 16 #9)
- token from Secret, never from env (spec § 16 #5)
- no NodePort / LoadBalancer; only egress through Cloudflare (spec § 16 #1)
- traefik service in `kube-system` does NOT terminate TLS (spec § 16 #2)

## Operating

See:

- `docs/features/winshare-subdomain-and-tunnel/spec.md` § 5.3 (tunnel ID rotation)
- `docs/features/winshare-subdomain-and-tunnel/spec.md` § 10 (failure modes)
- `docs/features/slo-and-alerting/spec.md` § 6.4 (`TunnelConnectorsLow` / `TunnelDown` alerts)
