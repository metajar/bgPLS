#!/usr/bin/env python3
"""Render FRR and SR Linux configs for the bgPLS Containerlab topology.

BGP-LS origination: FRR r1/r2 and SR Linux srl1/srl2 independently export the
shared IS-IS TED over AFI 16388/SAFI 71 toward bgPLS. The remaining FRR
routers are IS-IS only.
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

# name, node-id, collector ipv4, IS-IS ifaces: (port, ipv4, ipv6, metric, te-name)
SRL_NODES: list[dict] = [
    {
        "name": "srl1",
        "id": 9,
        "collector": ("10.0.3.1", "10.0.3.2/30"),
        "isis": [
            ("ethernet-1/2", "10.1.79.2/30", "fd00:1:79::2/64", 25, "to-r7"),
            ("ethernet-1/3", "10.1.90.1/30", "fd00:1:90::1/64", 20, "to-srl2"),
        ],
    },
    {
        "name": "srl2",
        "id": 10,
        "collector": ("10.0.4.1", "10.0.4.2/30"),
        "isis": [
            ("ethernet-1/2", "10.1.80.2/30", "fd00:1:80::2/64", 25, "to-r8"),
            ("ethernet-1/3", "10.1.90.2/30", "fd00:1:90::2/64", 20, "to-srl1"),
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
        " topology ipv6-unicast",
        " redistribute ipv4 connected level-2",
        " redistribute ipv6 connected level-2",
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


def srl_interface(iface: str, ipv4: str, ipv6: str = "", description: str = "") -> str:
    lines = [f"interface {iface} {{", "    admin-state enable"]
    if description:
        lines.append(f'    description "{description}"')
    lines.extend(
        [
            "    subinterface 0 {",
            "        admin-state enable",
            "        ipv4 {",
            "            admin-state enable",
            f"            address {ipv4} {{",
            "            }",
            "        }",
        ]
    )
    if ipv6:
        lines.extend(
            [
                "        ipv6 {",
                "            admin-state enable",
                f"            address {ipv6} {{",
                "            }",
                "        }",
            ]
        )
    lines.extend(["    }", "}"])
    return "\n".join(lines)


def srl_isis_iface(iface: str, metric: int, *, passive: bool = False) -> str:
    if passive:
        return "\n".join(
            [
                f"                interface {iface}.0 {{",
                "                    admin-state enable",
                "                    passive true",
                "                }",
            ]
        )
    return "\n".join(
        [
            f"                interface {iface}.0 {{",
            "                    admin-state enable",
            "                    circuit-type point-to-point",
            "                    ipv4-unicast {",
            "                        admin-state enable",
            "                    }",
            "                    ipv6-unicast {",
            "                        admin-state enable",
            "                    }",
            "                    level 2 {",
            f"                        metric {metric}",
            "                        timers {",
            "                            hello-interval 1",
            "                            hello-multiplier 3",
            "                        }",
            "                    }",
            "                }",
        ]
    )


def srl_te_iface(name: str, iface: str) -> str:
    return "\n".join(
        [
            f"        interface {name} {{",
            "            interface-ref {",
            f"                interface {iface}",
            "                subinterface 0",
            "            }",
            "        }",
        ]
    )


def render_srl(node: dict) -> str:
    name = node["name"]
    n = node["id"]
    lo = LOOPBACKS[n]
    lo6 = LOOPBACKS_V6[n]
    collector_peer, collector_local = node["collector"]
    collector_local_addr = collector_local.split("/")[0]
    isis_ifaces = node["isis"]

    iface_blocks = [
        srl_interface("ethernet-1/1", collector_local, description="BGP-LS to collector"),
    ]
    for iface, ipv4, ipv6, _metric, te_name in isis_ifaces:
        iface_blocks.append(srl_interface(iface, ipv4, ipv6, te_name.replace("to-", "IS-IS to ")))
    iface_blocks.append(srl_interface("lo0", f"{lo}/32", f"{lo6}/128", "system loopback"))

    ni_ifaces = ["    interface ethernet-1/1.0 {", "    }"]
    te_ifaces = []
    isis_ifaces_cfg = []
    for iface, _ipv4, _ipv6, metric, te_name in isis_ifaces:
        ni_ifaces.extend([f"    interface {iface}.0 {{", "    }"])
        te_ifaces.append(srl_te_iface(te_name, iface))
        isis_ifaces_cfg.append(srl_isis_iface(iface, metric))
    ni_ifaces.extend(["    interface lo0.0 {", "    }"])
    isis_ifaces_cfg.append(srl_isis_iface("lo0", 0, passive=True))

    return "\n".join(
        [
            f"# {name}: Nokia SR Linux IS-IS speaker and BGP-LS producer",
            *iface_blocks,
            "routing-policy {",
            "    policy accept-bgpls {",
            "        default-action {",
            "            accept {",
            "            }",
            "        }",
            "    }",
            "}",
            "network-instance default {",
            f"    router-id {lo}",
            *ni_ifaces,
            "    traffic-engineering {",
            "        autonomous-system 65000",
            f"        ipv4-te-router-id {lo}",
            f"        ipv6-te-router-id {lo6}",
            *te_ifaces,
            "    }",
            "    protocols {",
            "        isis {",
            "            instance 1 {",
            "                admin-state enable",
            "                level-capability L2",
            f"                net [ {isis_net(n)} ]",
            "                ipv4-unicast {",
            "                    admin-state enable",
            "                }",
            "                ipv6-unicast {",
            "                    admin-state enable",
            "                    multi-topology true",
            "                }",
            "                traffic-engineering {",
            "                    advertisement true",
            "                }",
            "                te-database-install {",
            "                    bgp-ls {",
            "                        igp-identifier 1",
            "                    }",
            "                }",
            *isis_ifaces_cfg,
            "            }",
            "        }",
            "        bgp {",
            "            admin-state enable",
            "            autonomous-system 65000",
            f"            router-id {lo}",
            "            afi-safi ipv4-unicast {",
            "                admin-state disable",
            "            }",
            "            afi-safi link-state {",
            "                admin-state enable",
            "                export-policy [",
            "                    accept-bgpls",
            "                ]",
            "            }",
            "            group bgpls {",
            "                admin-state enable",
            "                peer-as 65000",
            "                export-policy [",
            "                    accept-bgpls",
            "                ]",
            "                afi-safi ipv4-unicast {",
            "                    admin-state disable",
            "                }",
            "                afi-safi link-state {",
            "                    admin-state enable",
            "                    export-policy [",
            "                        accept-bgpls",
            "                    ]",
            "                    default-export-policy accept",
            "                }",
            "                transport {",
            f"                    local-address {collector_local_addr}",
            "                }",
            "                timers {",
            "                    hold-time 9",
            "                    keepalive-interval 3",
            "                }",
            "            }",
            f"            neighbor {collector_peer} {{",
            "                admin-state enable",
            "                peer-group bgpls",
            '                description "bgPLS collector"',
            "            }",
            "        }",
            "    }",
            "}",
            "",
        ]
    )


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
