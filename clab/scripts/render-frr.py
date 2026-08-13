#!/usr/bin/env python3
"""Render FRR configs for the bgPLS Containerlab topology."""

from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "frr" / "routers"

LOOPBACKS = {n: f"10.255.0.{n}" for n in range(1, 9)}
LOOPBACKS_V6 = {n: f"fd00:255::{n}" for n in range(1, 9)}

# iface, ipv4, ipv6, metric, delay_us, admin_grp, max_bw, isis
# max_bw is bytes/sec (10G=1250000000, 1G=125000000)
ROUTERS: dict[int, list[tuple[str, str, str, int, int, int, int, bool]]] = {
    1: [
        ("eth1", "10.0.1.2/30", "", 0, 0, 0, 0, False),
        ("eth2", "10.1.12.1/30", "fd00:1:12::1/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth3", "10.1.13.1/30", "fd00:1:13::1/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth4", "10.1.17.1/30", "fd00:1:17::1/64", 25, 800, 0x8, 125_000_000, True),
    ],
    2: [
        ("eth1", "10.0.2.2/30", "", 0, 0, 0, 0, False),
        ("eth2", "10.1.12.2/30", "fd00:1:12::2/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth3", "10.1.24.1/30", "fd00:1:24::1/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth4", "10.1.28.1/30", "fd00:1:28::1/64", 25, 800, 0x8, 125_000_000, True),
    ],
    3: [
        ("eth1", "10.1.13.2/30", "fd00:1:13::2/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth2", "10.1.34.1/30", "fd00:1:34::1/64", 15, 200, 0x2, 1_250_000_000, True),
        ("eth3", "10.1.35.1/30", "fd00:1:35::1/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth4", "10.1.36.1/30", "fd00:1:36::1/64", 20, 400, 0x4, 1_250_000_000, True),
    ],
    4: [
        ("eth1", "10.1.24.2/30", "fd00:1:24::2/64", 10, 100, 0x1, 1_250_000_000, True),
        ("eth2", "10.1.34.2/30", "fd00:1:34::2/64", 15, 200, 0x2, 1_250_000_000, True),
        ("eth3", "10.1.45.1/30", "fd00:1:45::1/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth4", "10.1.46.1/30", "fd00:1:46::1/64", 20, 400, 0x4, 1_250_000_000, True),
    ],
    5: [
        ("eth1", "10.1.35.2/30", "fd00:1:35::2/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth2", "10.1.45.2/30", "fd00:1:45::2/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth3", "10.1.56.1/30", "fd00:1:56::1/64", 15, 200, 0x2, 1_250_000_000, True),
        ("eth4", "10.1.57.1/30", "fd00:1:57::1/64", 25, 800, 0x8, 125_000_000, True),
    ],
    6: [
        ("eth1", "10.1.36.2/30", "fd00:1:36::2/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth2", "10.1.46.2/30", "fd00:1:46::2/64", 20, 400, 0x4, 1_250_000_000, True),
        ("eth3", "10.1.56.2/30", "fd00:1:56::2/64", 15, 200, 0x2, 1_250_000_000, True),
        ("eth4", "10.1.68.1/30", "fd00:1:68::1/64", 25, 800, 0x8, 125_000_000, True),
    ],
    7: [
        ("eth1", "10.1.17.2/30", "fd00:1:17::2/64", 25, 800, 0x8, 125_000_000, True),
        ("eth2", "10.1.57.2/30", "fd00:1:57::2/64", 25, 800, 0x8, 125_000_000, True),
        ("eth3", "10.1.78.1/30", "fd00:1:78::1/64", 30, 800, 0x8, 125_000_000, True),
    ],
    8: [
        ("eth1", "10.1.28.2/30", "fd00:1:28::2/64", 25, 800, 0x8, 125_000_000, True),
        ("eth2", "10.1.68.2/30", "fd00:1:68::2/64", 25, 800, 0x8, 125_000_000, True),
        ("eth3", "10.1.78.2/30", "fd00:1:78::2/64", 30, 800, 0x8, 125_000_000, True),
    ],
}

def isis_net(n: int) -> str:
    # 10.255.0.N -> 0102.5500.00NN
    return f"49.0001.0102.5500.{n:04d}.00"


def iface_block(iface: str, ipv4: str, ipv6: str, metric: int, delay: int, admin: int, bw: int, isis: bool) -> str:
    lines = [f"interface {iface}", f" ip address {ipv4}"]
    if ipv6:
        lines.append(f" ipv6 address {ipv6}")
    if isis:
        lines.extend(
            [
                " ip router isis 1",
                " ipv6 router isis 1",
                " isis network point-to-point",
                " isis circuit-type level-2",
                f" isis metric {metric}",
                " isis hello-interval 1",
                " isis hello-multiplier 4",
                " link-params",
                "  enable",
                f"  metric {metric}",
                f"  max-bw {bw}",
                f"  max-rsv-bw {bw}",
                f"  unrsv-bw 0 {bw}",
                f"  delay {delay}",
                f"  admin-grp {admin}",
                " exit-link-params",
            ]
        )
    lines.append("exit")
    return "\n".join(lines)


def bgp_block(n: int) -> str:
    rid = LOOPBACKS[n]
    lines = [
        "router bgp 65000",
        f" bgp router-id {rid}",
        " no bgp ebgp-requires-policy",
        " no bgp default ipv4-unicast",
        " bgp log-neighbor-changes",
    ]
    if n == 1:
        lines.extend(
            [
                " bgp cluster-id 10.255.0.1",
                " neighbor FABRIC peer-group",
                " neighbor FABRIC remote-as 65000",
                " neighbor FABRIC update-source lo",
                " neighbor FABRIC timers 3 9",
                " neighbor COLLECTOR peer-group",
                " neighbor COLLECTOR remote-as 65000",
                " neighbor COLLECTOR description bgPLS collector",
                " neighbor COLLECTOR passive",
                " neighbor COLLECTOR timers 3 9",
                " neighbor 10.0.1.1 peer-group COLLECTOR",
            ]
        )
        for peer in range(2, 9):
            lines.append(f" neighbor {LOOPBACKS[peer]} peer-group FABRIC")
        lines.extend(
            [
                " !",
                " address-family ipv4 unicast",
                "  no neighbor FABRIC activate",
                "  no neighbor COLLECTOR activate",
                " exit-address-family",
                " !",
                " address-family link-state",
                "  neighbor FABRIC activate",
                "  neighbor FABRIC route-reflector-client",
                "  neighbor COLLECTOR activate",
                " exit-address-family",
            ]
        )
    elif n == 2:
        lines.extend(
            [
                f" neighbor {LOOPBACKS[1]} remote-as 65000",
                f" neighbor {LOOPBACKS[1]} update-source lo",
                f" neighbor {LOOPBACKS[1]} description BGP-LS route reflector",
                " neighbor 10.0.2.1 remote-as 65000",
                " neighbor 10.0.2.1 description bgPLS collector",
                " neighbor 10.0.2.1 passive",
                " neighbor 10.0.2.1 timers 3 9",
                " !",
                " address-family ipv4 unicast",
                f"  no neighbor {LOOPBACKS[1]} activate",
                "  no neighbor 10.0.2.1 activate",
                " exit-address-family",
                " !",
                " address-family link-state",
                f"  neighbor {LOOPBACKS[1]} activate",
                "  neighbor 10.0.2.1 activate",
                " exit-address-family",
            ]
        )
    else:
        lines.extend(
            [
                f" neighbor {LOOPBACKS[1]} remote-as 65000",
                f" neighbor {LOOPBACKS[1]} update-source lo",
                f" neighbor {LOOPBACKS[1]} description BGP-LS route reflector",
                " !",
                " address-family ipv4 unicast",
                f"  no neighbor {LOOPBACKS[1]} activate",
                " exit-address-family",
                " !",
                " address-family link-state",
                f"  neighbor {LOOPBACKS[1]} activate",
                " exit-address-family",
            ]
        )
    lines.append("exit")
    return "\n".join(lines)


def render(n: int) -> str:
    name = f"r{n}"
    lo = LOOPBACKS[n]
    lo6 = LOOPBACKS_V6[n]
    blocks = [
        f"hostname {name}",
        "frr defaults datacenter",
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
        "exit",
    ]
    for iface in ROUTERS[n]:
        blocks.append("!")
        blocks.append(iface_block(*iface))
    blocks.extend(
        [
            "!",
            "router isis 1",
            f" net {isis_net(n)}",
            " is-type level-2-only",
            " metric-style wide",
            " hostname dynamic",
            " log-adjacency-changes",
            " topology ipv6-unicast",
            " lsp-gen-interval 5",
            " spf-interval 5",
            " mpls-te on",
            f" mpls-te router-address {lo}",
            " mpls-te export",
            "exit",
            "!",
            bgp_block(n),
            "!",
        ]
    )
    return "\n".join(blocks) + "\n"


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    for n in range(1, 9):
        path = OUT / f"r{n}.conf"
        path.write_text(render(n))
        print(f"wrote {path.relative_to(ROOT.parent)}")


if __name__ == "__main__":
    main()
