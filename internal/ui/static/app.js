(() => {
  const PAGE_SIZE = 1000;
  const POLL_MS = 4000;
  const PROTOCOLS = {
    PROTOCOL_ISIS_LEVEL_1: { label: "IS-IS L1", color: "#2dd4bf" },
    PROTOCOL_ISIS_LEVEL_2: { label: "IS-IS L2", color: "#38bdf8" },
    PROTOCOL_OSPFV2: { label: "OSPFv2", color: "#a78bfa" },
    PROTOCOL_OSPFV3: { label: "OSPFv3", color: "#c084fc" },
    PROTOCOL_BGP: { label: "BGP", color: "#fb7185" },
    PROTOCOL_DIRECT: { label: "Direct", color: "#94a3b8" },
    PROTOCOL_STATIC: { label: "Static", color: "#fbbf24" },
    PROTOCOL_UNSPECIFIED: { label: "Unknown", color: "#64748b" },
  };
  const FRESHNESS = {
    FRESHNESS_ACTIVE: "Active",
    FRESHNESS_STALE_SOURCE_LOST: "Stale (source lost)",
    FRESHNESS_WITHDRAWN: "Withdrawn",
    FRESHNESS_UNSPECIFIED: "Unspecified",
  };

  const els = {
    domain: document.getElementById("domain"),
    search: document.getElementById("search"),
    layout: document.getElementById("layout"),
    stale: document.getElementById("stale"),
    fit: document.getElementById("fit"),
    reload: document.getElementById("reload"),
    stats: document.getElementById("stats"),
    panel: document.getElementById("panel"),
    panelTitle: document.getElementById("panel-title"),
    panelBody: document.getElementById("panel-body"),
    panelClose: document.getElementById("panel-close"),
    empty: document.getElementById("empty"),
    error: document.getElementById("error"),
    legend: document.getElementById("legend"),
    pathSrc: document.getElementById("path-src"),
    pathDst: document.getElementById("path-dst"),
    pathMetric: document.getElementById("path-metric"),
    pathMinbw: document.getElementById("path-minbw"),
    pathCompute: document.getElementById("path-compute"),
    pathResult: document.getElementById("path-result"),
  };

  let cy;
  let prefixesByNode = new Map();
  let lastRevision = "";
  let pollTimer = 0;

  function num(v) {
    if (v == null || v === "") return 0;
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }

  function show(el, on) {
    el.hidden = !on;
    el.classList.toggle("hidden", !on);
  }

  async function connect(procedure, body) {
    const res = await fetch(procedure, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Connect-Protocol-Version": "1",
      },
      body: JSON.stringify(body || {}),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data.message || data.code || res.statusText);
    }
    return data;
  }

  async function listAll(procedure, key, extra) {
    const items = [];
    let pageToken = "";
    do {
      const msg = await connect(procedure, {
        ...extra,
        page: { pageSize: PAGE_SIZE, pageToken },
      });
      items.push(...(msg[key] || []));
      pageToken = (msg.page && msg.page.nextPageToken) || "";
    } while (pageToken);
    return items;
  }

  function filter() {
    const out = {};
    if (els.domain.value) out.domainIds = [els.domain.value];
    if (els.stale.checked) {
      out.freshness = ["FRESHNESS_ACTIVE", "FRESHNESS_STALE_SOURCE_LOST", "FRESHNESS_WITHDRAWN"];
    }
    return out;
  }

  function nodeLabel(n) {
    return n.name || n.igpRouterId || n.ipv4RouterId || n.bgpRouterId || (n.meta && n.meta.id) || "?";
  }

  function protocolInfo(p) {
    return PROTOCOLS[p] || PROTOCOLS.PROTOCOL_UNSPECIFIED;
  }

  function freshnessLabel(v) {
    return FRESHNESS[v] || v || "Unknown";
  }

  function utilClass(u) {
    if (!u || !u.utilizationKnown) return "util-unknown";
    const stale = u.staleAt && Date.parse(u.staleAt) < Date.now();
    if (stale) return "util-unknown";
    const r = Number(u.utilization) || 0;
    const load = Math.max(num(u.inBps), num(u.outBps));
    // Virt NICs often advertise 10G, so also color by absolute load for the lab heatmap.
    if (r > 0.8 || load >= 500e6) return "util-high";
    if (r >= 0.4 || load >= 50e6) return "util-mid";
    return "util-low";
  }

  function formatBits(bps) {
    const n = num(bps);
    if (!n) return "0";
    const units = ["bps", "Kbps", "Mbps", "Gbps", "Tbps"];
    let v = n;
    let i = 0;
    while (v >= 1000 && i < units.length - 1) {
      v /= 1000;
      i++;
    }
    return `${v >= 10 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
  }

  function formatUtilPct(u) {
    if (!u || !u.utilizationKnown) return "unknown";
    const pct = (Number(u.utilization) || 0) * 100;
    if (pct <= 0) return "0%";
    if (pct < 0.1) return "<0.1%";
    if (pct < 1) return `${pct.toFixed(2)}%`;
    return `${pct.toFixed(1)}%`;
  }

  function loadBps(u) {
    return Math.max(num(u && u.inBps), num(u && u.outBps));
  }

  function edgeLabel(link, u) {
    const metric = num(link.igpMetric);
    const parts = [];
    if (metric) parts.push(String(metric));
    if (u && u.utilizationKnown && loadBps(u) > 0) {
      parts.push(formatBits(loadBps(u)));
    }
    return parts.join(" · ");
  }

  function parseBw(s) {
    s = (s || "").trim().toUpperCase();
    if (!s) return 0;
    const m = s.match(/^([0-9.]+)\s*([KMG])?$/);
    if (!m) return 0;
    const n = Number(m[1]);
    const mult = m[2] === "G" ? 1e9 : m[2] === "M" ? 1e6 : m[2] === "K" ? 1e3 : 1;
    return Math.round(n * mult);
  }

  function formatBps(bytesPerSecond) {
    const bps = num(bytesPerSecond) * 8;
    if (!bps) return "";
    const units = ["bps", "Kbps", "Mbps", "Gbps", "Tbps"];
    let v = bps;
    let i = 0;
    while (v >= 1000 && i < units.length - 1) {
      v /= 1000;
      i++;
    }
    return `${v >= 10 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
  }

  function elementsFrom(nodes, links) {
    const known = new Set(nodes.map((n) => n.meta && n.meta.id).filter(Boolean));
    const elements = nodes.map((n) => {
      const proto = protocolInfo(n.protocol);
      const stale = n.meta && n.meta.freshness && n.meta.freshness !== "FRESHNESS_ACTIVE";
      const conflicts = n.meta && n.meta.conflicts && n.meta.conflicts.length > 0;
      return {
        data: {
          id: n.meta.id,
          label: nodeLabel(n),
          kind: "node",
          color: proto.color,
          stale,
          conflicts,
          entity: n,
        },
        classes: [n.pseudonode ? "pseudonode" : "router", stale ? "stale" : "", conflicts ? "conflict" : ""]
          .filter(Boolean)
          .join(" "),
      };
    });
    for (const l of links) {
      if (!l.localNodeId || !l.remoteNodeId || !known.has(l.localNodeId) || !known.has(l.remoteNodeId)) {
        continue;
      }
      const stale = l.meta && l.meta.freshness && l.meta.freshness !== "FRESHNESS_ACTIVE";
      const conflicts = l.meta && l.meta.conflicts && l.meta.conflicts.length > 0;
      const u = l.utilization || {};
      const uClass = utilClass(u);
      const speed = num(u.speedBps);
      const load = loadBps(u);
      const width = load >= 500e6 ? 4 : load >= 50e6 ? 3 : speed > 25e9 ? 3.2 : speed > 1e9 ? 2.2 : 1.6;
      elements.push({
        data: {
          id: l.meta.id,
          source: l.localNodeId,
          target: l.remoteNodeId,
          label: edgeLabel(l, u),
          kind: "link",
          stale,
          conflicts,
          entity: l,
          width,
        },
        classes: [stale ? "stale" : "", conflicts ? "conflict" : "", uClass].filter(Boolean).join(" "),
      });
    }
    return elements;
  }

  function style() {
    return [
      {
        selector: "node",
        style: {
          label: "data(label)",
          color: "#d7e0ea",
          "font-size": 11,
          "font-family": "ui-sans-serif, system-ui, sans-serif",
          "text-valign": "bottom",
          "text-margin-y": 6,
          "background-color": "data(color)",
          "border-width": 2,
          "border-color": "#0c1117",
          width: 18,
          height: 18,
        },
      },
      {
        selector: "node.pseudonode",
        style: { shape: "diamond", width: 14, height: 14 },
      },
      {
        selector: "node.stale",
        style: { opacity: 0.45 },
      },
      {
        selector: "node.conflict",
        style: { "border-color": "#f5b942", "border-width": 3 },
      },
      {
        selector: "node:selected",
        style: { "border-color": "#fff", "border-width": 3 },
      },
      {
        selector: "node.dim",
        style: { opacity: 0.15 },
      },
      {
        selector: "edge",
        style: {
          width: "data(width)",
          "curve-style": "bezier",
          "control-point-step-size": 24,
          "target-arrow-shape": "triangle",
          "target-arrow-color": "#5b6b7c",
          "line-color": "#5b6b7c",
          label: "data(label)",
          "font-size": 9,
          color: "#8b98a8",
          "text-wrap": "wrap",
          "text-background-color": "#0c1117",
          "text-background-opacity": 0.7,
          "text-background-padding": 2,
        },
      },
      {
        selector: "edge.stale",
        style: { "line-style": "dashed", opacity: 0.4 },
      },
      {
        selector: "edge.conflict",
        style: { "line-color": "#f5b942", "target-arrow-color": "#f5b942" },
      },
      {
        selector: "edge.util-unknown",
        style: { "line-color": "#64748b", "target-arrow-color": "#64748b" },
      },
      {
        selector: "edge.util-low",
        style: { "line-color": "#34d399", "target-arrow-color": "#34d399" },
      },
      {
        selector: "edge.util-mid",
        style: { "line-color": "#fbbf24", "target-arrow-color": "#fbbf24" },
      },
      {
        selector: "edge.util-high",
        style: { "line-color": "#f87171", "target-arrow-color": "#f87171" },
      },
      {
        selector: "edge.path",
        style: { width: 4, "line-color": "#e8eef6", "target-arrow-color": "#e8eef6" },
      },
      {
        selector: "edge.dim",
        style: { opacity: 0.08 },
      },
    ];
  }

  function layoutOptions(name) {
    if (name === "cose") {
      return { name: "cose", animate: true, animationDuration: 400, nodeRepulsion: 8000, idealEdgeLength: 90, gravity: 0.25 };
    }
    if (name === "breadthfirst") {
      return { name: "breadthfirst", animate: true, animationDuration: 300, directed: true, spacingFactor: 1.15 };
    }
    return { name, animate: true, animationDuration: 300, padding: 40 };
  }

  function ensureCy(elements, relayout) {
    if (!cy) {
      cy = cytoscape({
        container: document.getElementById("cy"),
        elements,
        style: style(),
        minZoom: 0.15,
        maxZoom: 4,
        wheelSensitivity: 0.3,
      });
      cy.on("tap", "node, edge", (ev) => showEntity(ev.target.data("kind"), ev.target.data("entity")));
      cy.on("tap", (ev) => {
        if (ev.target === cy) closePanel();
      });
      cy.layout(layoutOptions(els.layout.value)).run();
      return;
    }
    const positions = {};
    cy.nodes().forEach((n) => {
      positions[n.id()] = n.position();
    });
    cy.elements().remove();
    cy.add(elements);
    let restored = 0;
    cy.nodes().forEach((n) => {
      const pos = positions[n.id()];
      if (pos) {
        n.position(pos);
        restored++;
      }
    });
    if (relayout || restored === 0) {
      cy.layout(layoutOptions(els.layout.value)).run();
    }
  }

  function row(term, value) {
    if (value == null || value === "" || (Array.isArray(value) && value.length === 0)) return "";
    const dd = Array.isArray(value)
      ? `<dd class="list">${value.map((v) => `<span>${escapeHtml(String(v))}</span>`).join("")}</dd>`
      : `<dd>${escapeHtml(String(value))}</dd>`;
    return `<dt>${escapeHtml(term)}</dt>${dd}`;
  }

  function escapeHtml(s) {
    return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  function showEntity(kind, entity) {
    if (!entity) return;
    show(els.panel, true);
    if (kind === "link") {
      els.panelTitle.textContent = "Link";
      const m = entity.meta || {};
      els.panelBody.innerHTML = [
        row("ID", m.id),
        row("Domain", m.domainId),
        row("Freshness", freshnessLabel(m.freshness)),
        row("Local node", entity.localNodeId),
        row("Remote node", entity.remoteNodeId),
        row("Local IPv4", entity.localIpv4Address),
        row("Remote IPv4", entity.remoteIpv4Address),
        row("Local IPv6", entity.localIpv6Address),
        row("Remote IPv6", entity.remoteIpv6Address),
        row("IGP metric", entity.igpMetric),
        row("TE metric", entity.teMetric),
        row("Delay", entity.delayMicroseconds ? `${entity.delayMicroseconds} µs` : ""),
        row("Max bandwidth", formatBps(entity.maxBandwidthBytesPerSecond)),
        row("Reservable", formatBps(entity.reservableBandwidthBytesPerSecond)),
        row("MT-ID", entity.multiTopologyId),
        row("Admin groups", entity.adminGroups),
        row("SRLGs", entity.srlgs),
        row("Adjacency SIDs", entity.adjacencySids),
        row("Utilization", entity.utilization && entity.utilization.utilizationKnown
          ? `${formatUtilPct(entity.utilization)}  in ${formatBits(entity.utilization.inBps)} / out ${formatBits(entity.utilization.outBps)}`
          : "unknown"),
        row("Available", entity.utilization ? formatBits(entity.utilization.availableBps) : ""),
        row("Observed", entity.utilization && entity.utilization.observedAt),
        row("Sources", m.sourcePeerIds),
        row("Conflicts", (m.conflicts || []).map((c) => `${c.field}: ${c.selectedValue} vs ${c.rejectedValue}`)),
      ].join("");
      return;
    }
    const m = entity.meta || {};
    const hosted = prefixesByNode.get(m.id) || [];
    els.panelTitle.textContent = entity.pseudonode ? "Pseudonode" : "Node";
    els.panelBody.innerHTML = [
      row("Name", entity.name),
      row("ID", m.id),
      row("Domain", m.domainId),
      row("Protocol", protocolInfo(entity.protocol).label),
      row("Freshness", freshnessLabel(m.freshness)),
      row("AS", entity.autonomousSystem),
      row("Area", entity.areaId),
      row("IGP router ID", entity.igpRouterId),
      row("BGP router ID", entity.bgpRouterId),
      row("IPv4 router ID", entity.ipv4RouterId),
      row("IPv6 router ID", entity.ipv6RouterId),
      row("MT-IDs", entity.multiTopologyIds),
      row("Sources", m.sourcePeerIds),
      row("Conflicts", (m.conflicts || []).map((c) => `${c.field}: ${c.selectedValue} vs ${c.rejectedValue}`)),
      row("Prefixes", hosted.map((p) => p.prefix)),
    ].join("");
  }

  function closePanel() {
    show(els.panel, false);
    if (cy) cy.elements().unselect();
  }

  function applySearch() {
    if (!cy) return;
    const q = els.search.value.trim().toLowerCase();
    if (!q) {
      cy.elements().removeClass("dim");
      return;
    }
    const matched = cy.nodes().filter((n) => {
      const e = n.data("entity") || {};
      const m = e.meta || {};
      return [n.data("label"), m.id, e.igpRouterId, e.bgpRouterId, e.ipv4RouterId, e.ipv6RouterId, e.name]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q));
    });
    cy.elements().addClass("dim");
    matched.removeClass("dim");
    matched.connectedEdges().removeClass("dim");
    matched.neighborhood().removeClass("dim");
  }

  function setStat(name, text, visible) {
    const el = els.stats.querySelector(`[data-stat="${name}"]`);
    if (!el) return;
    el.textContent = text;
    el.classList.toggle("hidden", visible === false);
  }

  function renderLegend(nodes) {
    const seen = new Map();
    for (const n of nodes) {
      const info = protocolInfo(n.protocol);
      seen.set(info.label, info.color);
    }
    const items = [...seen.entries()].map(
      ([label, color]) => `<li><span class="swatch" style="--c:${color}"></span>${escapeHtml(label)}</li>`
    );
    items.push('<li><span class="swatch" style="--c:#34d399"></span>Util low / &lt;50 Mbps</li>');
    items.push('<li><span class="swatch" style="--c:#fbbf24"></span>Util 40–80% or ≥50 Mbps</li>');
    items.push('<li><span class="swatch" style="--c:#f87171"></span>Util &gt; 80% or ≥500 Mbps</li>');
    items.push('<li><span class="swatch" style="--c:#64748b"></span>Util unknown</li>');
    els.legend.innerHTML = items.join("");
  }

  async function loadDomains() {
    const domains = await listAll("/bgpls.v1.TopologyService/ListDomains", "domains", {});
    const current = els.domain.value;
    els.domain.innerHTML = `<option value="">All domains</option>` + domains
      .map((d) => `<option value="${escapeHtml(d.id)}">${escapeHtml(d.name || d.id)}</option>`)
      .join("");
    if ([...els.domain.options].some((o) => o.value === current)) {
      els.domain.value = current;
    }
  }

  async function loadGraph(relayout) {
    show(els.error, false);
    const f = filter();
    const [summary, nodes, links, prefixes] = await Promise.all([
      connect("/bgpls.v1.TopologyService/GetSummary", { filter: f }),
      listAll("/bgpls.v1.TopologyService/ListNodes", "nodes", { filter: f }),
      listAll("/bgpls.v1.TopologyService/ListLinks", "links", { filter: f }),
      listAll("/bgpls.v1.TopologyService/ListPrefixes", "prefixes", { filter: f }),
    ]);
    lastRevision = String(summary.revision || "");
    prefixesByNode = new Map();
    for (const p of prefixes) {
      const id = p.originNodeId;
      if (!id) continue;
      if (!prefixesByNode.has(id)) prefixesByNode.set(id, []);
      prefixesByNode.get(id).push(p);
    }
    setStat("revision", lastRevision ? `rev ${lastRevision}` : "rev —");
    setStat("nodes", `${num(summary.nodeCount)} nodes`);
    setStat("links", `${num(summary.linkCount)} links`);
    setStat("prefixes", `${num(summary.prefixCount)} prefixes`);
    setStat("conflicts", `${num(summary.conflictCount)} conflicts`, num(summary.conflictCount) > 0);
    setStat("stale", `${num(summary.staleCount)} stale`, num(summary.staleCount) > 0);
    const elements = elementsFrom(nodes, links);
    show(els.empty, nodes.length === 0);
    renderLegend(nodes);
    if (nodes.length === 0) {
      if (cy) {
        cy.elements().remove();
      }
      closePanel();
      return;
    }
    ensureCy(elements, relayout);
    applySearch();
    fillPathNodes(nodes);
  }

  function fillPathNodes(nodes) {
    if (!els.pathSrc) return;
    const opts = nodes.map((n) => {
      const id = n.meta && n.meta.id;
      const label = nodeLabel(n);
      return `<option value="${escapeHtml(id)}">${escapeHtml(label)}</option>`;
    });
    const src = els.pathSrc.value;
    const dst = els.pathDst.value;
    els.pathSrc.innerHTML = opts.join("");
    els.pathDst.innerHTML = opts.join("");
    if ([...els.pathSrc.options].some((o) => o.value === src)) els.pathSrc.value = src;
    if ([...els.pathDst.options].some((o) => o.value === dst)) els.pathDst.value = dst;
    if (!els.pathSrc.value && opts.length) els.pathSrc.selectedIndex = 0;
    if (!els.pathDst.value && opts.length > 1) els.pathDst.selectedIndex = Math.min(1, opts.length - 1);
  }

  async function computePath() {
    if (!els.pathSrc.value || !els.pathDst.value) return;
    const domain = els.domain.value || "core";
    const constraints = {};
    const minBw = parseBw(els.pathMinbw.value);
    if (minBw) constraints.minAvailableBps = String(minBw);
    els.pathResult.textContent = "Computing…";
    try {
      const msg = await connect("/bgpls.v1.PathService/ComputePaths", {
        domainId: domain,
        source: els.pathSrc.value,
        destination: els.pathDst.value,
        metric: els.pathMetric.value,
        constraints,
        maxPaths: 1,
      });
      if (!cy) return;
      cy.edges().removeClass("path");
      const paths = msg.paths || [];
      if (!paths.length) {
        els.pathResult.textContent = msg.explanation || "No path satisfies constraints";
        return;
      }
      const path = paths[0];
      const hopIds = [];
      for (const hop of path.hops || []) {
        const id = hop.outgoingLink && hop.outgoingLink.meta && hop.outgoingLink.meta.id;
        if (id) {
          hopIds.push(id);
          const edge = cy.getElementById(id);
          if (edge) edge.addClass("path");
        }
      }
      const avail = formatBits(path.bottleneckAvailableBps);
      const stale = path.usedStaleData ? " (stale data used)" : "";
      els.pathResult.textContent = `metric ${path.totalMetric || 0}  bottleneck ${avail}${stale}\n${hopIds.length} hops`;
    } catch (err) {
      els.pathResult.textContent = err.message || String(err);
    }
  }

  async function pollUtilization() {
    if (!cy) return;
    try {
      const msg = await connect("/bgpls.v1.EnrichmentService/GetLinkUtilization", {});
      let hottest = 0;
      for (const u of msg.links || []) {
        hottest = Math.max(hottest, loadBps(u));
        const edge = cy.getElementById(u.linkId);
        if (!edge || !edge.length) continue;
        const entity = edge.data("entity") || {};
        entity.utilization = u;
        edge.data("entity", entity);
        edge.data("label", edgeLabel(entity, u));
        const load = loadBps(u);
        edge.data("width", load >= 500e6 ? 4 : load >= 50e6 ? 3 : num(edge.data("width")) || 1.6);
        edge.removeClass("util-unknown util-low util-mid util-high");
        edge.addClass(utilClass(u));
      }
      setStat("util", hottest ? `hot ${formatBits(hottest)}` : "util idle");
    } catch {
      // overlay may be empty during startup
    }
  }

  async function refresh(relayout) {
    try {
      await loadDomains();
      await loadGraph(relayout);
    } catch (err) {
      show(els.error, true);
      els.error.textContent = err.message || String(err);
    }
  }

  async function poll() {
    try {
      const summary = await connect("/bgpls.v1.TopologyService/GetSummary", { filter: filter() });
      if (String(summary.revision || "") !== lastRevision) {
        await loadGraph(false);
      }
    } catch {
      // Keep the last good canvas; the next explicit reload surfaces the error.
    }
  }

  els.domain.addEventListener("change", () => refresh(true));
  els.stale.addEventListener("change", () => refresh(true));
  els.layout.addEventListener("change", () => {
    if (cy) cy.layout(layoutOptions(els.layout.value)).run();
  });
  els.search.addEventListener("input", applySearch);
  els.fit.addEventListener("click", () => {
    if (cy) cy.fit(undefined, 40);
  });
  els.reload.addEventListener("click", () => refresh(true));
  els.panelClose.addEventListener("click", closePanel);
  if (els.pathCompute) els.pathCompute.addEventListener("click", computePath);
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") closePanel();
    if (ev.key === "/" && document.activeElement !== els.search) {
      ev.preventDefault();
      els.search.focus();
    }
  });

  refresh(true);
  pollTimer = window.setInterval(poll, POLL_MS);
  window.setInterval(pollUtilization, 2000);
  window.addEventListener("beforeunload", () => window.clearInterval(pollTimer));
})();
