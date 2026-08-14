# Architecture

The data path is deliberately layered:

```text
GoBGP BGP-LS Adj-RIB-In
          |
          v
NLRI/attribute translation -> per-peer advertisements
          |
          v
deterministic reconciliation -> canonical node/link/prefix mutations
          |
          +--> Pebble current state + revision event journal
          |
          +--> revision-scoped graph and prefix indexes
                         |
                         v
             ConnectRPC/gRPC APIs, CLI, and /ui
```

Live interface utilization is a separate overlay (see
[utilization.md](utilization.md)). Samples never create topology revisions.

Stable IDs hash normalized BGP-LS key descriptors, including domain, protocol,
instance identifier, router descriptors, link descriptors, MT-ID, and prefix.
Mutable attributes such as node names and metrics never participate in identity.

Every accepted mutation is written with its event and new revision in one synced
Pebble batch. API snapshots therefore name a revision with no partially visible
change. Historical reads undo later retained events from current state; watches
first replay retained events and then subscribe to live revisions.

The graph is directed. Constraint evaluation treats missing required bandwidth,
TE metric, or delay as ineligible. Domain filtering occurs before path or impact
calculation, preventing accidental automatic cross-domain stitching.
