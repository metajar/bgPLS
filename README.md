# bgPLS

bgPLS is a standalone BGP Link-State collector and topology query service. It
embeds GoBGP for BGP-LS sessions, normalizes node/link/prefix advertisements into
a revisioned graph, persists current state and change history in Pebble, and
serves ConnectRPC and gRPC APIs secured with mutual TLS.

For a copy-paste setup from local smoke test through a real BGP-LS peer, see the
[quick-start guide](docs/quickstart.md).

## Current capabilities

- BGP-LS node, directed link, IPv4 prefix, and IPv6 prefix collection through GoBGP.
- TE/IGP metrics, delay, bandwidth, administrative groups, SRLGs, prefix SIDs,
  adjacency SIDs, multi-topology IDs, router identities, and recognized TLVs in
  raw form.
- Multiple producer reconciliation with deterministic source preference and
  withdrawal fallback.
- Persistent revisions, resumable event watches, point-in-time reconstruction,
  topology diffs, and configurable age/size retention.
- Current and historical topology APIs, constrained shortest path queries, and
  node/link failure impact analysis.
- Persistent peer CRUD, session control, config import/export, TCP MD5, GTSM,
  eBGP multihop, and receive-only import/export policy.
- TLS 1.3 mutual authentication with URI/DNS SAN role mappings for reader,
  operator, and admin access.
- JSON-oriented CLI, Prometheus endpoint, structured logging, health check, and
  atomic TLS/RBAC reload on `SIGHUP`.

## Containerlab test fabric

On a Linux host with Docker and [Containerlab](https://containerlab.dev/install/), clone this repository and run:

```sh
make clab
```

That deploys eight FRR routers, a bgPLS collector, and lab mTLS certificates. When it finishes, query BGP-LS topology immediately:

```sh
./clab/scripts/bgpls.sh topology summary
./clab/scripts/bgpls.sh topology nodes --domain core
./clab/scripts/bgpls.sh path compute --domain core --source r1 --destination r8 --metric igp
```

The API listens on `https://127.0.0.1:7443`. See [clab/README.md](clab/README.md) for the topology, certificates, and destroy/status commands.

## Build and test

Requirements are Go 1.26 and Buf 1.71 or newer.

```sh
make generate
make check
```

Generated Protobuf and ConnectRPC Go sources are committed, so consumers do not
need `protoc` to build the binary.

## Configure and run

Copy `bgpls.example.yaml` and replace the example addresses and certificate
paths. The production listener requires a server certificate, private key, at
least one client CA, and SAN-to-role mappings.

```sh
cp bgpls.example.yaml bgpls.yaml
./bgpls serve --config bgpls.yaml
```

For a local-only development instance, use:

```yaml
api:
  listen: 127.0.0.1:7443
  tls:
    development_insecure: true
```

`development_insecure` is rejected on a non-loopback address.
Use an `http://127.0.0.1:7443` CLI server URL in this development mode.

Example requests:

```sh
bgpls topology summary --server https://bgpls.example.net:7443 \
  --ca ca.crt --cert operator.crt --key operator.key
bgpls topology nodes --domain core --server https://bgpls.example.net:7443 \
  --ca ca.crt --cert reader.crt --key reader.key
bgpls path compute --domain core --source r1 --destination r9 --metric igp \
  --server https://bgpls.example.net:7443 --ca ca.crt \
  --cert reader.crt --key reader.key
bgpls history diff --after 1000 --before 2000 --server https://bgpls.example.net:7443 \
  --ca ca.crt --cert reader.crt --key reader.key
```

The Protobuf contracts are under `proto/bgpls/v1`. ConnectRPC also accepts
native gRPC and gRPC-Web requests on the same service paths.

## Operational notes

- The YAML peer list bootstraps only an empty database. Runtime API changes are
  authoritative afterward; use `peers export` and `peers import` for GitOps.
- A data directory must only be opened by one bgPLS process.
- Retained event history defaults to 30 days or 50 GB. Current state is separate
  and is never removed by history pruning.
- Point-in-time reconstruction currently reverses retained events from current
  state. The store interface leaves room for periodic materialized checkpoints
  without changing the API.
- HA replication, cross-domain path stitching, SR/SRv6 SID-stack generation,
  alerting, and a web UI remain post-v1 work.
- Unsupported optional BGP-LS extensions are intentionally ignored. They do not
  invalidate supported node, link, prefix, or TE data, and bounded Prometheus
  counters expose accepted, stale, withdrawn, ignored, and rejected paths.

Collector-specific metrics include `bgpls_bgp_ls_paths_total` and
`bgpls_bgp_ls_decode_errors_total`. Their labels are bounded to configured peer
IDs, path state, entity kind, and fixed error classes; raw error messages never
become metric labels.
