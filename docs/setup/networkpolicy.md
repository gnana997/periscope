# NetworkPolicy

Periscope ships a `NetworkPolicy` template that is **off by default**. Every
cluster has different ingress controller plumbing and IdP egress targets,
so a one-size policy would either be too loose to be useful or too tight to
work anywhere. Enable it once you know your environment.

## What you get when enabled

Default-deny ingress and egress on the Periscope pod, then:

- **Ingress**: only from the namespaces you list in
  `networkPolicy.ingress.fromNamespaces`. Each entry is a
  `namespaceSelector.matchLabels` map.
- **Egress**:
  - DNS to `kube-dns` (always added — without it nothing else resolves).
  - Anything you supply in `networkPolicy.egress.extra` (typically the
    IdP host CIDRs, the EKS API endpoints for the clusters you manage,
    and AWS STS).

## Minimum useful values

```yaml
networkPolicy:
  enabled: true
  ingress:
    fromNamespaces:
      - kubernetes.io/metadata.name: ingress-nginx
  egress:
    extra:
      # IdP — replace with your actual issuer host CIDRs (Okta/Auth0
      # publish ranges; for self-hosted Keycloak, point at the Service).
      - to:
          - ipBlock:
              cidr: 0.0.0.0/0
              except:
                - 10.0.0.0/8
                - 172.16.0.0/12
                - 192.168.0.0/16
        ports:
          - protocol: TCP
            port: 443
```

The `0.0.0.0/0` minus RFC1918 trick is the lazy way to allow "all internet
HTTPS but no in-cluster traffic." For tighter posture, list the actual
IdP and EKS endpoint CIDRs explicitly.

## Why no per-cluster EKS endpoint rule by default

EKS API server endpoints are public IPs that AWS rotates. We cannot
template a stable rule from chart values. Operators typically either:

1. Allow all egress to TCP/443 (above), accepting that the pod can reach
   anything on the internet over HTTPS.
2. Run a DNS-aware egress proxy (Cilium FQDN policy, AWS VPC endpoints
   for EKS) and point Periscope at that.

Option 2 is the right answer for regulated environments; option 1 is the
common starting point.

## Verifying

After enabling:

```sh
kubectl -n <ns> describe networkpolicy <release>-periscope
kubectl -n <ns> exec deploy/<release>-periscope -- wget -qO- http://localhost:8080/healthz
kubectl -n <ns> logs deploy/<release>-periscope | grep -i 'oidc\|provider'
```

If OIDC discovery fails after enabling the policy, your `egress.extra`
isn't reaching the issuer.
