# Containerlab topology

Clone this repository on a Linux host with Docker and [Containerlab](https://containerlab.dev/install/), then run:

```sh
make clab
```

That builds the collector image, mints lab mTLS certificates, deploys eight FRR routers plus bgPLS, and waits until BGP-LS topology is visible. Go is not required on the lab host; the collector image is built inside Docker.

## What you get

- Eight FRR 10.7 routers in a dual-core / dual-edge IS-IS Level-2 fabric with IPv4 and IPv6 loopbacks and mixed IGP metrics.
- r1 and r2 independently originate the IS-IS traffic-engineering database over BGP-LS (AFI 16388/SAFI 71). r3-r8 are IS-IS only.
- The collector peers with r1 and r2 over dedicated links that are not in IS-IS.
- mTLS API on `https://127.0.0.1:7443` and Prometheus metrics on `http://127.0.0.1:9090/metrics`.

```text
                    collector
                   /         \
                 r1 --------- r2
                /  \         /  \
              r3 -- r4     r5 -- r6
               |  X  |     |  X  |
              r7 ----+-----+---- r8
```

## Query the API

`make clab` copies a `./bgpls` CLI onto the lab host. Use the wrapper so certificates are passed automatically:

```sh
./clab/scripts/bgpls.sh topology summary
./clab/scripts/bgpls.sh topology nodes --domain core
./clab/scripts/bgpls.sh topology links --domain core
./clab/scripts/bgpls.sh topology prefixes --domain core
./clab/scripts/bgpls.sh path compute --domain core --source r1 --destination r8 --metric igp
./clab/scripts/bgpls.sh peers list
```

If node names have not appeared yet, use loopback addresses:

```sh
./clab/scripts/bgpls.sh path compute --domain core --source 10.255.0.1 --destination 10.255.0.8 --metric igp
```

Equivalent explicit invocation:

```sh
./bgpls topology summary \
  --server https://127.0.0.1:7443 \
  --ca clab/pki/ca.crt \
  --cert clab/pki/admin.crt \
  --key clab/pki/admin.key
```

Client certificates are `admin`, `operator`, and `reader` under `clab/pki`. Set `BGPLS_ROLE=reader` on the wrapper to use the reader identity. From another machine, copy `clab/pki` and use `--server https://<lab-host>:7443`.

## Operations

```sh
make clab-status
make clab-destroy
```

If Containerlab must run as root:

```sh
make clab CONTAINERLAB="sudo containerlab"
```

`make clab` recreates the lab and wipes `clab/data` so the YAML peer list is bootstrapped again. FRR 10.7 or newer is required because that is the first FRR release that originates BGP-LS from the IGP TED.
