#!/usr/bin/env python3
"""Render FRR and SR Linux configs for the bgPLS Containerlab topology.

BGP-LS origination: FRR r1/r2 export the shared IS-IS TED over AFI 16388/SAFI 71
toward bgPLS. SR Linux srl1/srl2 are IS-IS only; 7220 IXR-D has no BGP-LS AFI
and no MT-ISIS, so IPv6 stays in topology MT0 on both FRR and SR Linux.
"""

from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FRR_OUT = ROOT / "frr" / "routers"
SRL_OUT = ROOT / "srl"

LOOPBACKS = {n: f"10.255.0.{n}" for n in range(1, 11)}
LOOPBACKS_V6 = {n: f"fd00:255::{n}" for n in range(1, 11)}
COLLECTOR = {1: "10.0.1.1", 2: "10.0.2.1"}

# iface, ipv4, ipv6, metric, isis
ROUTERS: dict[int, list[tuple[str, str, str, int, bool]]] = {
    1: [
        ("eth1", "10.0.1.2/30", "", 0, False),
        ("eth2", "10.1.12.1/30", "fd00:1:12::1/64", 10, True),
        ("eth3", "10.1.13.1/30", "fd00:1:13::1/64", 10, True),
        ("eth4", "10.1.17.1/30", "fd00:1:17::1/64", 25, True),
    ],
    2: [
        ("eth1", "10.0.2.2/30", "", 0, False),
        ("eth2", "10.1.12.2/30", "fd00:1:12::2/64", 10, True),
        ("eth3", "10.1.24.1/30", "fd00:1:24::1/64", 10, True),
        ("eth4", "10.1.28.1/30", "fd00:1:28::1/64", 25, True),
    ],
    3: [
        ("eth1", "10.1.13.2/30", "fd00:1:13::2/64", 10, True),
        ("eth2", "10.1.34.1/30", "fd00:1:34::1/64", 15, True),
        ("eth3", "10.1.35.1/30", "fd00:1:35::1/64", 20, True),
        ("eth4", "10.1.36.1/30", "fd00:1:36::1/64", 20, True),
    ],
    4: [
        ("eth1", "10.1.24.2/30", "fd00:1:24::2/64", 10, True),
        ("eth2", "10.1.34.2/30", "fd00:1:34::2/64", 15, True),
        ("eth3", "10.1.45.1/30", "fd00:1:45::1/64", 20, True),
        ("eth4", "10.1.46.1/30", "fd00:1:46::1/64", 20, True),
    ],
    5: [
        ("eth1", "10.1.35.2/30", "fd00:1:35::2/64", 20, True),
        ("eth2", "10.1.45.2/30", "fd00:1:45::2/64", 20, True),
        ("eth3", "10.1.56.1/30", "fd00:1:56::1/64", 15, True),
        ("eth4", "10.1.57.1/30", "fd00:1:57::1/64", 25, True),
    ],
    6: [
        ("eth1", "10.1.36.2/30", "fd00:1:36::2/64", 20, True),
        ("eth2", "10.1.46.2/30", "fd00:1:46::2/64", 20, True),
        ("eth3", "10.1.56.2/30", "fd00:1:56::2/64", 15, True),
        ("eth4", "10.1.68.1/30", "fd00:1:68::1/64", 25, True),
    ],
    7: [
        ("eth1", "10.1.17.2/30", "fd00:1:17::2/64", 25, True),
        ("eth2", "10.1.57.2/30", "fd00:1:57::2/64", 25, True),
        ("eth3", "10.1.78.1/30", "fd00:1:78::1/64", 30, True),
        ("eth4", "10.1.79.1/30", "fd00:1:79::1/64", 25, True),
    ],
    8: [
        ("eth1", "10.1.28.2/30", "fd00:1:28::2/64", 25, True),
        ("eth2", "10.1.68.2/30", "fd00:1:68::2/64", 25, True),
        ("eth3", "10.1.78.2/30", "fd00:1:78::2/64", 30, True),
        ("eth4", "10.1.80.1/30", "fd00:1:80::1/64", 25, True),
    ],
}

# name, node-id, IS-IS ifaces: (port, ipv4, ipv6, metric, te-name)
SRL_NODES: list[dict] = [
    {
        "name": "srl1",
        "id": 9,
        "isis": [
            ("ethernet-1/1", "10.1.79.2/30", "fd00:1:79::2/64", 25, "to-r7"),
            ("ethernet-1/2", "10.1.90.1/30", "fd00:1:90::1/64", 20, "to-srl2"),
        ],
    },
    {
        "name": "srl2",
        "id": 10,
        "isis": [
            ("ethernet-1/1", "10.1.80.2/30", "fd00:1:80::2/64", 25, "to-r8"),
            ("ethernet-1/2", "10.1.90.2/30", "fd00:1:90::2/64", 20, "to-srl1"),
        ],
    },
]


def isis_net(n: int) -> str:
    return f"49.0001.0000.0000.{n:04d}.00"


def iface_block(iface: str, ipv4: str, ipv6: str, metric: int, isis: bool) -> str:
    lines = [f"interface {iface}", f" ip address {ipv4}"]
    if ipv6:
        lines.append(f" ipv6 address {ipv6}")
    if isis:
        lines.extend(
            [
                " link-params",
                " exit-link-params",
                " ip router isis 1",
                " ipv6 router isis 1",
                " isis circuit-type level-2",
                " isis hello-interval 1",
                " isis hello-multiplier 3",
                " no isis hello padding",
                " isis network point-to-point",
                f" isis metric {metric}",
            ]
        )
    return "\n".join(lines)


def bgp_block(n: int) -> str:
    neighbor = COLLECTOR[n]
    rid = LOOPBACKS[n]
    return "\n".join(
        [
            "router bgp 65000",
            f" bgp router-id {rid}",
            " no bgp ebgp-requires-policy",
            " no bgp default ipv4-unicast",
            f" neighbor {neighbor} remote-as 65000",
            f" neighbor {neighbor} description bgPLS collector",
            f" neighbor {neighbor} timers 3 9",
            " !",
            " address-family link-state",
            f"  neighbor {neighbor} activate",
            " exit-address-family",
        ]
    )


def render_frr(n: int) -> str:
    name = f"r{n}"
    lo = LOOPBACKS[n]
    lo6 = LOOPBACKS_V6[n]
    producer = n in COLLECTOR
    blocks = [
        f"hostname {name}",
        "log stdout informational",
        "service integrated-vtysh-config",
        "ip forwarding",
        "ipv6 forwarding",
        "!",
        "interface lo",
        f" ip address {lo}/32",
        f" ipv6 address {lo6}/128",
        " ip router isis 1",
        " ipv6 router isis 1",
        " isis passive",
    ]
    for iface in ROUTERS[n]:
        blocks.append("!")
        blocks.append(iface_block(*iface))
    isis = [
        "!",
        "router isis 1",
        f" net {isis_net(n)}",
        " is-type level-2-only",
        " hostname dynamic",
        " log-adjacency-changes",
        " metric-style wide",
        " redistribute ipv4 connected level-2",
        " redistribute ipv6 connected level-2",
        " lsp-mtu 1492",
        " lsp-gen-interval 1",
        " lsp-refresh-interval 10",
        " max-lsp-lifetime 350",
        " mpls-te on",
        f" mpls-te router-address {lo}",
        f" mpls-te router-address ipv6 {lo6}",
    ]
    if producer:
        isis.append(" mpls-te export")
    isis.append("!")
    blocks.extend(isis)
    if producer:
        blocks.extend([bgp_block(n), "!"])
    return "\n".join(blocks) + "\n"


def srl_set_iface(iface: str, ipv4: str, ipv6: str = "", description: str = "") -> list[str]:
    lines = [f"set / interface {iface} admin-state enable"]
    if description:
        lines.append(f'set / interface {iface} description "{description}"')
    lines.extend(
        [
            f"set / interface {iface} subinterface 0 admin-state enable",
            f"set / interface {iface} subinterface 0 ipv4 admin-state enable",
            f"set / interface {iface} subinterface 0 ipv4 address {ipv4}",
        ]
    )
    if ipv6:
        lines.extend(
            [
                f"set / interface {iface} subinterface 0 ipv6 admin-state enable",
                f"set / interface {iface} subinterface 0 ipv6 address {ipv6}",
            ]
        )
    return lines


def srl_set_isis_iface(iface: str, metric: int, *, passive: bool = False) -> list[str]:
    prefix = f"set / network-instance default protocols isis instance 1 interface {iface}.0"
    lines = [f"{prefix} admin-state enable"]
    if passive:
        lines.append(f"{prefix} passive true")
        return lines
    lines.extend(
        [
            f"{prefix} circuit-type point-to-point",
            f"{prefix} ipv4-unicast admin-state enable",
            f"{prefix} ipv6-unicast admin-state enable",
            f"{prefix} level 2 metric {metric}",
            f"{prefix} level 2 timers hello-interval 1",
            f"{prefix} level 2 timers hello-multiplier 3",
        ]
    )
    return lines


def render_srl(node: dict) -> str:
    name = node["name"]
    n = node["id"]
    lo = LOOPBACKS[n]
    lo6 = LOOPBACKS_V6[n]
    isis_ifaces = node["isis"]
    inst = "set / network-instance default protocols isis instance 1"

    lines = [f"# {name}: Nokia SR Linux IS-IS speaker (BGP-LS is not on 7220 IXR-D)"]
    for iface, ipv4, ipv6, _metric, te_name in isis_ifaces:
        lines.extend(srl_set_iface(iface, ipv4, ipv6, te_name.replace("to-", "IS-IS to ")))
    lines.extend(srl_set_iface("system0", f"{lo}/32", f"{lo6}/128", "system loopback"))
    lines.append(f"set / network-instance default router-id {lo}")
    for iface, _ipv4, _ipv6, _metric, _te_name in isis_ifaces:
        lines.append(f"set / network-instance default interface {iface}.0")
    lines.extend(
        [
            "set / network-instance default interface system0.0",
            f"{inst} admin-state enable",
            f"{inst} level-capability L2",
            f"{inst} net [{isis_net(n)}]",
            f"{inst} hello-padding disable",
            f"{inst} ipv4-unicast admin-state enable",
            f"{inst} ipv6-unicast admin-state enable",
            f"{inst} ipv6-unicast multi-topology false",
            f"{inst} level 2 metric-style wide",
        ]
    )
    for iface, _ipv4, _ipv6, metric, _te_name in isis_ifaces:
        lines.extend(srl_set_isis_iface(iface, metric))
    lines.extend(srl_set_isis_iface("system0", 0, passive=True))
    lines.append("")
    return "\n".join(lines)


def main() -> None:
    FRR_OUT.mkdir(parents=True, exist_ok=True)
    SRL_OUT.mkdir(parents=True, exist_ok=True)
    for n in range(1, 9):
        path = FRR_OUT / f"r{n}.conf"
        path.write_text(render_frr(n))
        print(f"wrote {path.relative_to(ROOT.parent)}")
    for node in SRL_NODES:
        path = SRL_OUT / f"{node['name']}.cli"
        path.write_text(render_srl(node))
        print(f"wrote {path.relative_to(ROOT.parent)}")


if __name__ == "__main__":
    main()
