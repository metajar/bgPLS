# Containerlab topology

Clone this repository on a Linux host with Docker and [Containerlab](https://containerlab.dev/install/), then run:

```sh
make clab
```

That builds the collector and utilcol image, mints lab mTLS certificates, deploys eight FRR routers plus two SR Linux nodes, snmpd, iperf3 generators, bgPLS, and the utilization poller, and waits until BGP-LS topology is visible. Go is not required on the lab host; images are built inside Docker. The first deploy pulls `ghcr.io/nokia/srlinux:26.7.1-554`.

## What you get

- Eight FRR 10.7 routers and two Nokia SR Linux 26.7 nodes in a dual-core / dual-edge IS-IS Level-2 fabric with IPv4 and IPv6 loopbacks and mixed IGP metrics.
- r1 and r2 originate the mixed FRR/SR Linux IS-IS TED over BGP-LS (AFI 16388/SAFI 71). r3-r8, srl1, and srl2 are IS-IS only. 7220 IXR-D does not implement BGP-LS.
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
               |                 |
             srl1 ------------- srl2
```

## Query the API

`make clab` copies a `./bgpls` CLI onto the lab host. From the repository root it loads `clab/pki` automatically:

```sh
./bgpls topology summary
./bgpls topology nodes --domain core
./bgpls topology links --domain core
./bgpls topology prefixes --domain core
./bgpls path compute --domain core --source r1 --destination srl2 --metric igp
./bgpls topology links --domain core --show-utilization
./clab/scripts/traffic.sh demo
```

If node names have not appeared yet, use loopback addresses:

```sh
./clab/scripts/bgpls.sh path compute --domain core --source 10.255.0.1 --destination 10.255.0.10 --metric igp
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

## Traffic and utilization demo

`tgen1` sits behind r1 (`10.10.1.0/24`) and `tgen2` behind srl2 (`10.10.2.0/24`).
FRR nodes run snmpd; SR Linux is scraped over gNMI. `utilcol` reports rates to
bgPLS every 10s.

```sh
./clab/scripts/traffic.sh steady --rate 200M
./clab/scripts/traffic.sh surge --rate 900M --duration 60
./clab/scripts/traffic.sh demo
./clab/scripts/traffic.sh stop
```

`demo` starts a baseline flow, computes an IGP path with a live available-bandwidth
constraint that still qualifies, surges, and checks that CSPF moves off the hot
link (or returns no-path). Open `http://127.0.0.1:8080/ui/` to watch link colors
and the path tester. See [docs/utilization.md](../docs/utilization.md).

## Operations

```sh
make clab-status
make clab-destroy
```

If Containerlab must run as root:

```sh
make clab CONTAINERLAB="sudo containerlab"
```

`make clab` recreates the lab and wipes `clab/data` so the YAML peer list is bootstrapped again. FRR 10.7 or newer is required because that is the first FRR release that originates BGP-LS from the IGP TED. SR Linux nodes use `ixr-d3l` (7220 IXR-D3L). That platform speaks IS-IS with FRR but does not implement BGP-LS or MT-ISIS; Nokia documents those only on 7250 IXR / 7730 SXR. The fabric therefore keeps IPv4 and IPv6 in IS-IS topology MT0.
