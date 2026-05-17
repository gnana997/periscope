# exec-client

Companion artifact for the Periscope blog post on pod exec —
*WebSocket up, SPDY fallback*. A small Go program that demonstrates
the protocol negotiation a production K8s console has to implement
because not every apiserver speaks both protocols.

## What it does

Connects to `/api/v1/namespaces/<ns>/pods/<pod>/exec`, picks one of:

- **`--protocol=spdy`** — `remotecommand.NewSPDYExecutor`, the
  ten-year-default for `kubectl exec`. Works on every apiserver.
- **`--protocol=ws`** — `remotecommand.NewWebSocketExecutor`. Beta
  in K8s 1.31 behind the `RemoteCommandWebsocket` feature gate,
  GA target 1.32–1.33. Fails on older or feature-gate-off clusters.
- **`--protocol=auto`** (default) — tries WS first; on a WebSocket
  upgrade refusal (`httpstream.IsUpgradeFailure`), falls back to
  SPDY. Logs every step to stderr so the negotiation is visible.

The negotiation log lines go to **stderr**, the exec'd command's
output goes to **stdout**. So pipes work cleanly:

```bash
exec-client --protocol=auto > output.txt   # only the command's output
exec-client --protocol=auto 2> trace.log   # only the negotiation trace
```

## Prerequisites

- Go 1.24+
- `kubectl` configured with a cluster you can exec into
- A pod named `busybox-probe` in the `default` namespace (or pass
  `--pod` / `--namespace` for a different target)

If you've already run the impersonation-client demo, you have a
`busybox-probe` pod sitting in `default` — this example reuses it.

## Quickstart against your existing cluster

```bash
make run-auto      # tries WS first, falls back to SPDY if refused
make run-ws        # forces WS — errors if your apiserver doesn't speak it
make run-spdy      # forces SPDY — always works
```

Override the context with `CONTEXT=`:

```bash
make CONTEXT=kind-mycluster run-auto
```

Or the target command:

```bash
make CMD="hostname; date; cat /proc/1/status | head -3" run-auto
```

## The fallback demo — two kind clusters

To actually exercise the fallback path, you need an apiserver that
*refuses* the WebSocket upgrade. K8s 1.31+ has it on by default, so
the only way to demo refusal is a cluster with the feature gate
explicitly disabled. Two configs in `manifests/`:

```bash
make cluster-ws            # creates `exec-ws`, feature gate ON
make cluster-no-ws         # creates `exec-no-ws`, feature gate OFF

# Provision the target pod on each cluster:
kubectl --context=kind-exec-ws apply \
  -f ../impersonation-client/manifests/
kubectl --context=kind-exec-no-ws apply \
  -f ../impersonation-client/manifests/

# Now compare:
make CONTEXT=kind-exec-ws    run-auto    # → protocol=WebSocket result=ok
make CONTEXT=kind-exec-no-ws run-auto    # → WS refused — falling back to SPDY
                                         # → protocol=SPDY result=ok (fallback)
```

Cleanup:

```bash
make cluster-ws-down
make cluster-no-ws-down
```

**Caveat**: KEP-4006's GA target is K8s 1.32 / 1.33. Once GA'd, the
`RemoteCommandWebsocket` feature gate is *removed* and toggling it
via apiserver flags is a hard error. If `cluster-no-ws` fails to
create, pin to an older node image — uncomment the `image:` line in
`manifests/kind-ws-off.yaml` and adjust the tag.

## Seeing the negotiation on the wire

The blog post pairs this binary with Wireshark captures of both
handshakes. The shortcut for seeing the headers without packet
capture is `kubectl --v=8`:

```bash
kubectl --context=kind-exec-ws --v=8 \
  exec busybox-probe -- uname -a 2>&1 \
  | grep -iE 'upgrade|connection'
```

Look for:

- `Upgrade: SPDY/3.1` on the SPDY path
- `Upgrade: websocket` + `Sec-WebSocket-Protocol: v5.channel.k8s.io`
  on the WS path

The exec-client binary uses the exact same client-go code paths as
`kubectl exec`, so what you see on the wire matches one-for-one.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--context` | (current) | kubeconfig context |
| `--kubeconfig` | `$KUBECONFIG` or `~/.kube/config` | kubeconfig path |
| `--namespace` | `default` | target pod namespace |
| `--pod` | `busybox-probe` | target pod name |
| `--container` | (pod's first) | target container |
| `--command` | `uname -a; ls /` | shell command (wrapped with `sh -c`) |
| `--protocol` | `auto` | `auto` \| `ws` \| `spdy` |
