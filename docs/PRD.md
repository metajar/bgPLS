# bgPLS v1 Product Requirements

## Purpose

bgPLS continuously learns BGP Link-State advertisements and turns them into a
durable, revisioned network graph. Network engineers, automation, and AI agents
must be able to inspect current topology, compute paths, analyze failures, and
compare topology over time without querying individual routers.

## Required outcomes

- Run as one Linux binary with an embedded database and no mandatory external
  service.
- Collect non-VPN and VPN BGP-LS node, directed-link, and IPv4/IPv6 prefix
  advertisements from multiple peers and topology domains.
- Retain source provenance, reconcile duplicate producers deterministically,
  expose conflicts, honor withdrawals, and distinguish active from stale data.
- Provide current and point-in-time topology, revision diffs, resumable change
  streams, constrained same-domain paths, and failure impact analysis.
- Serve versioned Protobuf contracts through ConnectRPC and native gRPC using
  TLS 1.3 mutual authentication and SAN-based reader/operator/admin roles.
- Persist peer operations made through the API; use YAML only to bootstrap an
  uninitialized database and for explicit import/export.
- Default to 30 days or 50 GB of history while never pruning current state.

## Data contract

Canonical entities are `Domain`, `Peer`, `Node`, `Link`, `Prefix`, and
`TopologyEvent`. Every topology entity has a stable descriptor-derived ID,
domain, first/last observation time, freshness, source peer IDs, conflicts,
best-effort decoded TLV diagnostics, and the revision that last changed it. Parallel and unidirectional links
remain distinct. Domains are never automatically stitched for path queries.

Typed fields cover router identities, areas and ASNs, link/interface addresses,
MT-ID, IGP/TE/delay metrics, bandwidth, administrative groups, SRLGs, and the SR
identifiers decoded by the selected BGP engine. Unsupported optional extensions
are ignored without rejecting an otherwise valid node, link, or prefix. They are
not required for topology correctness or retained for propagation.

## API and operations

The `bgpls.v1` schema defines topology, path, history, and collector services.
Large topology reads and live watches use server streaming; list methods are
bounded and paginated. Responses identify their revision. Peer updates use
optimistic resource versions.

The first-party CLI covers health, topology inventory, paths, changes/diffs,
and peer lifecycle. Production deployments expose structured logs, Prometheus
metrics, certificate/RBAC reload on `SIGHUP`, crash-safe store recovery, and
receive-only BGP export policy.

## Acceptance criteria

- Protocol fixtures and fuzzing cover valid, malformed, duplicate, withdrawn,
  multi-topology, TE, and SR inputs. Unsupported optional extensions must not
  prevent supported topology data from being applied.
- Crash recovery reproduces the latest committed revision; retained historical
  revisions reproduce earlier values.
- Path tests cover directed and parallel links, pseudonodes, ECMP, missing
  metrics, bandwidth/admin-group constraints, and unreachable destinations.
- Security tests reject missing/untrusted identities and enforce all three roles.
- Reference-scale qualification targets 100 peers, 100,000 nodes, 1,000,000
  links, several million prefixes, 10,000 updates/second, p95 visibility under
  two seconds, and p95 current path queries under 500 ms on 16 cores, 64 GB RAM,
  and local NVMe.

## Explicit non-goals

v1 does not program a FIB, advertise topology, provide HA clustering, perform
automatic cross-domain routing, generate SR/SRv6 SID stacks, implement a full
PCE, send alerts, or ship a web visualization.

## Production qualification

The repository implements the runnable v1 architecture and primary workflows.
Before declaring production qualification complete, add independent-router wire
fixtures, decoder fuzzing, reference-scale benchmarks, and materialized
historical checkpoints. Exact retention of unrecognized TLVs is explicitly not
required.
