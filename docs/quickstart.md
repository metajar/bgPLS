# bgPLS quick start

This guide gets bgPLS running locally first, then connects it to a real BGP-LS
speaker. The fastest setup uses an active BGP connection from bgPLS to a route
reflector and a loopback-only, plaintext API while validating the deployment.

## 1. Prerequisites

You need:

- Linux on amd64 or arm64.
- Go 1.26 or newer to build from source.
- IP reachability from the bgPLS host to TCP port 179 on a router or route
  reflector that exports BGP-LS.
- The local and remote AS numbers for that BGP session.

The upstream speaker must support and advertise BGP-LS AFI 16388, SAFI 71. A
normal IPv4 or IPv6 unicast BGP session will not provide topology information.

## 2. Build the binary

From the repository root:

```sh
make build
./bgpls version
```

Generated API sources are committed, so Buf and `protoc` are not required for a
normal build. `make check` runs the schema checks and test suite if Buf is also
installed.

## 3. Validate the service locally

Create `bgpls.quickstart.yaml`:

```yaml
data_dir: ./quickstart-data

api:
  listen: 127.0.0.1:7443
  metrics_listen: 127.0.0.1:9090
  tls:
    development_insecure: true

bgp:
  router_id: 192.0.2.10
  listen_port: -1

retention:
  duration: 720h
  max_bytes: 53687091200

domains:
  - id: core
    name: Core network
```

Choose a unique IPv4 router ID for `bgp.router_id`. `listen_port: -1` disables
passive BGP listening; bgPLS will initiate configured peer sessions instead.

Start the service in one terminal:

```sh
./bgpls serve --config bgpls.quickstart.yaml
```

In a second terminal, verify the API and store:

```sh
./bgpls health --server http://127.0.0.1:7443
./bgpls topology summary --server http://127.0.0.1:7443
curl -s http://127.0.0.1:9090/metrics | head
```

`SERVING` and an empty topology are expected at this point. This proves the
binary, API, and embedded database work; it does not yet prove BGP-LS ingestion.

## 4. Add a BGP-LS peer

Stop the local smoke-test process with `Ctrl-C`. Because the previous step has
already initialized its store, change the configuration to a fresh network data
directory:

```yaml
data_dir: ./quickstart-network-data
```

Then add the peer to `bgpls.quickstart.yaml`:

```yaml
peers:
  - id: core-rr1
    domain_id: core
    name: Core route reflector 1
    remote_address: 10.0.0.1
    local_address: 10.0.0.10
    local_as: 65000
    remote_as: 65000
    source_preference: 100
    enabled: true
```

Replace the example addresses and AS numbers. `local_address` may be omitted if
the operating system should choose the source address.

For directly connected eBGP, use different AS numbers. For multihop eBGP, add:

```yaml
    ebgp_multihop: true
    multihop_ttl: 8
```

For TCP MD5, add the same secret configured on the router:

```yaml
    tcp_md5_secret: replace-with-the-shared-secret
```

Do not enable GTSM and eBGP multihop together. For a single-hop GTSM session,
use `gtsm: true` instead.

On the router or route reflector, configure a matching neighbor with:

- Neighbor address equal to the bgPLS source address.
- Remote AS equal to `local_as` in the bgPLS configuration.
- BGP-LS/link-state address family enabled for the neighbor.
- Export policy that permits the desired BGP-LS node, link, and prefix NLRIs.
- Matching TCP MD5 authentication if configured.

The exact router syntax is vendor- and release-specific. Verify in the router's
BGP summary that the negotiated address family is BGP-LS, not only IPv4/IPv6
unicast.

Start bgPLS again after editing the bootstrap configuration:

```sh
./bgpls serve --config bgpls.quickstart.yaml
```

The YAML peer list is used only when the data store is uninitialized. Once a
store contains state, peer changes are authoritative through the API/CLI. For an
existing deployment, use the peer commands described under "Managing peers
after bootstrap."

## 5. Confirm topology collection

Check the peer first:

```sh
./bgpls peers list --server http://127.0.0.1:7443
```

The peer should reach `PEER_SESSION_STATE_ESTABLISHED`. Then query the topology:

```sh
./bgpls topology summary --server http://127.0.0.1:7443
./bgpls topology nodes --domain core --server http://127.0.0.1:7443
./bgpls topology links --domain core --server http://127.0.0.1:7443
./bgpls topology prefixes --domain core --server http://127.0.0.1:7443
```

After at least two connected nodes are present, compute a path using a stable
node ID, router name, router ID, or an IP contained in an advertised prefix:

```sh
./bgpls path compute \
  --domain core \
  --source router-a \
  --destination router-b \
  --metric igp \
  --server http://127.0.0.1:7443
```

Inspect collector outcomes through Prometheus:

```sh
curl -s http://127.0.0.1:9090/metrics | \
  grep '^bgpls_bgp_ls_'
```

Useful metrics are:

- `bgpls_bgp_ls_paths_total`: accepted, withdrawn, stale, ignored, and rejected
  BGP-LS paths by peer and entity kind.
- `bgpls_bgp_ls_decode_errors_total`: bounded error categories for paths that
  could not be normalized.

Unsupported optional extensions are ignored. They do not prevent supported
nodes, links, prefixes, or traffic-engineering attributes from being collected.

## 6. Managing peers after bootstrap

List and inspect the current persistent peer resources:

```sh
./bgpls peers list --server http://127.0.0.1:7443
./bgpls peers get --id core-rr1 --server http://127.0.0.1:7443
```

Export the persistent configuration:

```sh
./bgpls peers export \
  --file peers.yaml \
  --server http://127.0.0.1:7443
```

Import an edited export without deleting peers omitted from the file:

```sh
./bgpls peers import \
  --file peers.yaml \
  --server http://127.0.0.1:7443
```

Add `--replace` only when the file should become the complete peer set. The
export deliberately omits TCP MD5 secrets; an empty imported secret preserves
the stored value for an existing peer.

Peer enable, disable, update, and delete operations require the current
`resource_version` returned by `peers get`, which prevents accidental concurrent
overwrites.

## 7. Enable mTLS for production

Do not expose `development_insecure` beyond loopback. For production, provision
a server certificate and client certificates from your private PKI. The server
certificate must cover the hostname or IP clients use, and client identity is
read from URI or DNS SANs—not the certificate common name.

Example production API configuration:

```yaml
api:
  listen: 0.0.0.0:7443
  metrics_listen: 127.0.0.1:9090
  tls:
    certificate: /etc/bgpls/server.crt
    private_key: /etc/bgpls/server.key
    client_cas:
      - /etc/bgpls/client-ca.crt
  rbac:
    - role: admin
      uri_sans:
        - spiffe://example.net/bgpls/admin/*
    - role: operator
      uri_sans:
        - spiffe://example.net/bgpls/operator/*
    - role: reader
      uri_sans:
        - spiffe://example.net/bgpls/reader/*
```

Call the secured API with:

```sh
./bgpls topology summary \
  --server https://bgpls.example.net:7443 \
  --ca /etc/bgpls/server-ca.crt \
  --cert reader.crt \
  --key reader.key
```

Send `SIGHUP` after rotating the API certificate, client CA, or RBAC mappings.
bgPLS reloads those values without restarting BGP sessions.

## Troubleshooting

### The peer never establishes

Check TCP port 179 reachability, both AS numbers, the selected source address,
multihop TTL, GTSM, and TCP MD5. Confirm the router permits the bgPLS neighbor
and that no other BGP process is using the same source identity.

If using passive mode, set `passive: true` on the peer and configure
`bgp.listen_port: 179`. Binding a privileged port normally requires root or the
`CAP_NET_BIND_SERVICE` capability. Active mode with `listen_port: -1` avoids
that requirement.

### The session is established but the topology is empty

Confirm AFI 16388/SAFI 71 was negotiated and that the upstream export policy
permits BGP-LS. Verify that the upstream device is actually producing or
reflecting its IGP topology. Then inspect the bgPLS logs and
`bgpls_bgp_ls_paths_total`.

### A YAML peer edit has no effect

This is expected after the first successful bootstrap. Use `peers import`, the
peer CRUD commands, or point the lab configuration at a new empty `data_dir`.

### The API rejects the client certificate

Verify the certificate is signed by a configured client CA and that a URI or DNS
SAN matches the required role. Topology and history queries need `reader`, peer
session operations need `operator`, and peer CRUD/import/export need `admin`.

### Data survives a restart

That is expected. Current topology, peers, revisions, and retained history are
stored under `data_dir`. Only one bgPLS process may open a data directory at a
time.
