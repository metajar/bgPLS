# Live utilization overlay

bgPLS collects BGP-LS topology with static TE attributes. IGP-TE unreserved
bandwidth is a reservation ledger, not a traffic counter, so in an SR-TE
network without RSVP it is effectively static. Live interface utilization is
therefore a **separate overlay**: it never flows through BGP-LS or the
revisioned event history.

```text
SR Linux  --gNMI Subscribe-->  utilcol  --ReportInterfaceUtilization-->  bgPLS overlay
FRR       --SNMP poll------->     ^
                                  |
                          mTLS, operator role
```

`utilcol` (`cmd/utilcol`) holds device credentials. bgPLS never does. Reports
identify an interface by device name, ifName, and configured IPv4/IPv6
addresses. bgPLS joins those addresses to directed BGP-LS links by **local
interface IP**.

## Correlation

1. An in-memory index maps each local link address (`local_address`,
   `local_ipv4_address`, `local_ipv6_address`) to a directed link ID.
2. The first matching address wins. The interface `out_bps` is the load of that
   directed link; `in_bps` is attached to the reverse directed link when it
   exists.
3. If one address matches links from **different local nodes**, the report is
   dropped (ambiguity guard) and counted. That usually means duplicate
   addressing or a decode bug.
4. Unnumbered interfaces (no IP) are out of scope. They appear in
   `GetUncorrelatedInterfaces` with reason `unnumbered`.

With the Containerlab fabric and utilcol running, at least 95% of BGP-LS links
should correlate within 60 seconds of both processes starting, with zero
ambiguity-guard hits.

## Staleness

The overlay stores only the latest sample per link. Default `stale_after` is
45s (~four missed 10s samples). Reads past `stale_at` still return the record;
CSPF follows `stale_policy`:

- `TREAT_AS_UNKNOWN` (default): the available-bandwidth constraint passes and
  the path is flagged `used_stale_data`.
- `FAIL_LINK`: prune links with stale or missing utilization.

A sweeper deletes samples older than `sweep_after` (default 10 minutes). Missing
utilization must not break path computation: CSPF degrades to static TE.

## Path computation

`PathConstraints.min_available_bps` prunes links whose live `available_bps`
(`speed − max(in,out)`) is below the threshold. Metric `PATH_METRIC_AVAILABLE_BW`
selects the widest path (maximize the bottleneck available bandwidth, tie-break
by IGP).

```sh
bgpls path compute --domain core --source r1 --destination srl2 \
  --metric igp --min-available-bw 500M --stale-policy fail
bgpls topology links --domain core --show-utilization
```

## Lab demo

`make clab` starts snmpd on the FRR nodes, gNMI on SR Linux (`insecure-mgmt` on
port 57401, plaintext), utilcol, and two
iperf3 traffic generators (`tgen1` behind r1, `tgen2` behind srl2). Then:

```sh
./clab/scripts/traffic.sh demo
```

That starts a baseline UDP flow, computes a path, surges, and checks that CSPF
moves off the hot link (or returns no-path). Lab iperf is UDP because TCP on
the Containerlab SR Linux path tops out around 70 Mbps regardless of `--rate`.
