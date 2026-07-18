// known explore — vanilla JS frontend for the read-only graph explorer.
// Talks to the Go API via relative `api/...` paths only. No build step.
"use strict";

/* ------------------------------------------------------------------ *
 * Constants
 * ------------------------------------------------------------------ */

const EDGE_TYPE_COLORS = {
  "depends-on": "#d9822b",
  "elaborates": "#4a90d9",
  "related-to": "#8a8f98",
  "supersedes": "#9b59b6",
  "contradicts": "#e0473e",
};
const CUSTOM_EDGE_COLOR = "#2fbf9f"; // teal fallback for any non-predefined edge type
const PREDEFINED_EDGE_TYPES = ["depends-on", "contradicts", "supersedes", "elaborates", "related-to"];

const SUPERSEDED_OPACITY = 0.45;
const IMPLICIT_EDGE_OPACITY = 0.55;
const NODE_MIN_SIZE = 22;
const NODE_MAX_SIZE = 64;
const NODE_SIZE_PER_DEGREE = 5;

const ERROR_BANNER_MS = 6000;
const PATH_NOTICE_MS = 4000;
const SEARCH_DEBOUNCE_MS = 250;

/* ------------------------------------------------------------------ *
 * DOM references
 * ------------------------------------------------------------------ */

const el = {
  errorBanner: document.getElementById("error-banner"),
  errorText: document.getElementById("error-banner-text"),
  errorClose: document.getElementById("error-banner-close"),
  pathNotice: document.getElementById("path-notice"),
  scopeSelect: document.getElementById("scope-select"),
  labelSelect: document.getElementById("label-select"),
  searchInput: document.getElementById("search-input"),
  searchResults: document.getElementById("search-results"),
  reloadBtn: document.getElementById("reload-btn"),
  pathModeBtn: document.getElementById("path-mode-btn"),
  legend: document.getElementById("legend"),
  cyContainer: document.getElementById("cy"),
  panel: document.getElementById("detail-panel"),
  panelTitle: document.getElementById("panel-title"),
  panelClose: document.getElementById("panel-close"),
  panelBody: document.getElementById("panel-body"),
  edgePopover: document.getElementById("edge-popover"),
};

/* ------------------------------------------------------------------ *
 * Error / notice banners — every fetch failure funnels through here.
 * ------------------------------------------------------------------ */

let errorTimer = null;
function showError(message) {
  el.errorText.textContent = String(message);
  el.errorBanner.classList.remove("hidden");
  clearTimeout(errorTimer);
  errorTimer = setTimeout(() => el.errorBanner.classList.add("hidden"), ERROR_BANNER_MS);
}
el.errorClose.addEventListener("click", () => {
  el.errorBanner.classList.add("hidden");
  clearTimeout(errorTimer);
});

let noticeTimer = null;
function showNotice(message) {
  el.pathNotice.textContent = message;
  el.pathNotice.classList.remove("hidden");
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => el.pathNotice.classList.add("hidden"), PATH_NOTICE_MS);
}

// Last-resort safety net: nothing should ever reach the console only.
window.addEventListener("error", (e) => {
  showError("Unexpected error: " + (e.message || e));
});
window.addEventListener("unhandledrejection", (e) => {
  const reason = e.reason && e.reason.message ? e.reason.message : String(e.reason);
  showError("Unexpected error: " + reason);
  e.preventDefault();
});

/* ------------------------------------------------------------------ *
 * API layer — relative paths only, uniform error surfacing.
 * ------------------------------------------------------------------ */

async function apiFetch(path) {
  let res;
  try {
    res = await fetch(path);
  } catch (err) {
    const msg = "Network error fetching " + path + ": " + err.message;
    showError(msg);
    throw new Error(msg);
  }
  let body = null;
  try {
    body = await res.json();
  } catch (err) {
    const msg = "Malformed response from " + path;
    showError(msg);
    throw new Error(msg);
  }
  if (!res.ok || (body && typeof body === "object" && "error" in body)) {
    const msg = (body && body.error) || ("Request failed (" + res.status + "): " + path);
    showError(msg);
    throw new Error(msg);
  }
  return body;
}

function qs(params) {
  const parts = [];
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
  }
  return parts.length ? "?" + parts.join("&") : "";
}

const api = {
  meta: () => apiFetch("api/meta"),
  graph: (scope, label) => apiFetch("api/graph" + qs({ scope, label })),
  entry: (id) => apiFetch("api/entry/" + encodeURIComponent(id)),
  neighbors: (id, direction, depth) =>
    apiFetch("api/neighbors/" + encodeURIComponent(id) + qs({ direction, depth })),
  search: (q, scope) => apiFetch("api/search" + qs({ q, scope })),
  path: (from, to) => apiFetch("api/path" + qs({ from, to })),
};

/* ------------------------------------------------------------------ *
 * Color helpers
 * ------------------------------------------------------------------ */

function edgeColor(type) {
  return EDGE_TYPE_COLORS[type] || CUSTOM_EDGE_COLOR;
}

// Deterministic string -> HSL hue so arbitrary scope segments get a stable,
// visually distinct color without maintaining an exhaustive palette.
// FNV-1a accumulation + a Murmur3-style avalanche finalizer, then a
// golden-ratio multiplicative spread before the mod -- a plain `h % 360`
// on a weak accumulator clusters similar short strings (e.g. "frontend"
// vs "backend") within a few degrees of each other; this spreads them
// across the hue circle instead.
function avalanche32(h) {
  h ^= h >>> 16;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return h >>> 0;
}
function hashHue(str) {
  let h = 0x811c9dc5; // FNV-1a 32-bit offset basis
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193); // FNV prime
  }
  h = avalanche32(h);
  return Math.floor(((h * 0.6180339887498949) % 1) * 360);
}
function scopeColor(scope) {
  const first = (scope || "").split("/")[0] || "(none)";
  return "hsl(" + hashHue(first) + ", 55%, 55%)";
}

/* ------------------------------------------------------------------ *
 * Cytoscape setup
 * ------------------------------------------------------------------ */

const cy = cytoscape({
  container: el.cyContainer,
  wheelSensitivity: 0.2,
  style: [
    {
      selector: "node",
      style: {
        "background-color": "data(_color)",
        "label": "data(_label)",
        "color": "#e6e8eb",
        "text-outline-color": "#14161a",
        "text-outline-width": 2,
        "font-size": 10,
        "width": "data(_size)",
        "height": "data(_size)",
        "border-width": "data(_borderWidth)",
        "border-color": "data(_borderColor)",
        "opacity": "data(_opacity)",
        "text-valign": "bottom",
        "text-margin-y": 4,
      },
    },
    {
      selector: "node:selected",
      style: { "border-color": "#ffd23f", "border-width": 4 },
    },
    {
      selector: "node.path-endpoint",
      style: { "border-color": "#ffd23f", "border-width": 4 },
    },
    {
      selector: "edge",
      style: {
        "width": "data(_width)",
        "line-color": "data(_color)",
        "target-arrow-color": "data(_color)",
        "target-arrow-shape": "triangle",
        "arrow-scale": 1,
        "curve-style": "bezier",
        "opacity": "data(_opacity)",
        "line-style": "data(_lineStyle)",
      },
    },
    {
      selector: "edge:selected",
      style: { "line-color": "#ffd23f", "target-arrow-color": "#ffd23f", "width": "data(_width)" },
    },
    { selector: ".path-dim", style: { opacity: 0.1 } },
    { selector: ".path-highlight", style: { opacity: 1, "z-index": 999 } },
  ],
  layout: { name: "preset" },
});

/* ------------------------------------------------------------------ *
 * Graph state — cytoscape's own model is the single source of truth.
 * Merging never duplicates nodes/edges: add if absent, else refresh data.
 * ------------------------------------------------------------------ */

function nodeToEleData(node) {
  return {
    id: node.id,
    title: node.title || "",
    content: node.content || "",
    scope: node.scope || "",
    labels: node.labels || [],
    source_type: node.source_type || "",
    created_at: node.created_at || "",
    updated_at: node.updated_at || "",
    observed_at: node.observed_at || "",
  };
}

function edgeToEleData(edge) {
  return {
    id: edge.id,
    source: edge.from,
    target: edge.to,
    type: edge.type,
    weight: edge.weight,
    explicit: edge.explicit,
    created_at: edge.created_at,
  };
}

function mergeGraph(graph) {
  if (!graph) return;
  cy.batch(() => {
    for (const node of graph.nodes || []) {
      const existing = cy.getElementById(node.id);
      if (existing.length) {
        existing.data(nodeToEleData(node));
      } else {
        cy.add({ group: "nodes", data: nodeToEleData(node) });
      }
    }
    for (const edge of graph.edges || []) {
      const existing = cy.getElementById(edge.id);
      if (existing.length) {
        existing.data(edgeToEleData(edge));
      } else {
        // both endpoints are guaranteed present in graph.nodes per contract
        cy.add({ group: "edges", data: edgeToEleData(edge) });
      }
    }
  });
  recomputeDerived();
}

function replaceGraph(graph) {
  cy.elements().remove();
  mergeGraph(graph);
}

// Recompute all client-derived visual state over the whole current graph:
// degree-based size, per-scope color, conflict border, superseded fade,
// edge width/color/opacity/dash. Cheap at explorer scale; avoids bugs from
// incremental partial recomputation.
function recomputeDerived() {
  cy.batch(() => {
    cy.nodes().forEach((n) => {
      const degree = n.connectedEdges().length;
      const size = Math.max(NODE_MIN_SIZE, Math.min(NODE_MAX_SIZE, NODE_MIN_SIZE + degree * NODE_SIZE_PER_DEGREE));
      const hasConflict = n.connectedEdges('[type = "contradicts"]').length > 0;
      const isSuperseded = n.connectedEdges('[type = "supersedes"]').some((e) => e.data("target") === n.id());
      const title = n.data("title");
      const content = n.data("content") || "";
      const label = title || (content ? content.slice(0, 40) : "") || "(untitled)";
      n.data({
        _color: scopeColor(n.data("scope")),
        _size: size,
        _borderColor: hasConflict ? "#e0473e" : "#454b58",
        _borderWidth: hasConflict ? 3 : 1,
        _opacity: isSuperseded ? SUPERSEDED_OPACITY : 1,
        _label: label,
        _conflict: hasConflict,
        _superseded: isSuperseded,
      });
    });
    cy.edges().forEach((e) => {
      const weight = typeof e.data("weight") === "number" ? e.data("weight") : 0;
      e.data({
        _color: edgeColor(e.data("type")),
        _width: 1 + 4 * weight,
        _opacity: e.data("explicit") ? 1 : IMPLICIT_EDGE_OPACITY,
        _lineStyle: e.data("type") === "contradicts" ? "dashed" : "solid",
      });
    });
  });
}

function runLayout(randomize) {
  cy.layout({
    name: "cose",
    randomize,
    fit: true,
    animate: false,
    nodeRepulsion: 400000,
    idealEdgeLength: 90,
    numIter: 1000,
  }).run();
}

/* ------------------------------------------------------------------ *
 * Toolbar: meta, scope/label filters, reload
 * ------------------------------------------------------------------ */

let currentMeta = null;

function populateSelect(select, values, selected, allLabel) {
  select.innerHTML = "";
  const allOpt = document.createElement("option");
  allOpt.value = "";
  allOpt.textContent = allLabel;
  select.appendChild(allOpt);
  for (const v of values) {
    const opt = document.createElement("option");
    opt.value = v;
    opt.textContent = v;
    select.appendChild(opt);
  }
  select.value = values.includes(selected) ? selected : "";
}

function renderLegend() {
  el.legend.innerHTML = "";
  const entries = PREDEFINED_EDGE_TYPES.map((t) => [t, EDGE_TYPE_COLORS[t], t === "contradicts"]);
  entries.push(["custom", CUSTOM_EDGE_COLOR, false]);
  for (const [label, color, dashed] of entries) {
    const item = document.createElement("div");
    item.className = "legend-item";
    const swatch = document.createElement("span");
    swatch.className = "legend-swatch";
    swatch.style.borderTopColor = color;
    swatch.style.borderTopStyle = dashed ? "dashed" : "solid";
    const text = document.createElement("span");
    text.textContent = label;
    item.appendChild(swatch);
    item.appendChild(text);
    el.legend.appendChild(item);
  }
  addNodeLegendItem("scope color", (swatch) => {
    swatch.style.background = "hsl(210, 55%, 55%)";
  });
  addNodeLegendItem("conflict border", (swatch) => {
    swatch.style.background = "#454b58";
    swatch.style.borderColor = "#e0473e";
    swatch.style.borderWidth = "2px";
  });
  addNodeLegendItem("superseded (faded)", (swatch) => {
    swatch.style.background = "#454b58";
    swatch.style.opacity = "0.45";
  });
}

function addNodeLegendItem(label, styleSwatch) {
  const item = document.createElement("div");
  item.className = "legend-item";
  const swatch = document.createElement("span");
  swatch.className = "legend-node-swatch";
  styleSwatch(swatch);
  const text = document.createElement("span");
  text.textContent = label;
  item.appendChild(swatch);
  item.appendChild(text);
  el.legend.appendChild(item);
}

async function loadMeta() {
  const meta = await api.meta();
  currentMeta = meta;
  populateSelect(el.scopeSelect, meta.scopes || [], meta.default_scope || "", "(all scopes)");
  populateSelect(el.labelSelect, meta.labels || [], "", "(all labels)");
  renderLegend();
}

async function loadGraph() {
  const scope = el.scopeSelect.value;
  const label = el.labelSelect.value;
  const graph = await api.graph(scope, label);
  replaceGraph(graph);
  runLayout(true);
  if (graph.truncated) {
    showNotice("Graph truncated: result set exceeded the server limit.");
  }
}

async function doReload() {
  closePanel();
  try {
    await loadGraph();
  } catch (_) {
    // already surfaced via showError
  }
}

el.reloadBtn.addEventListener("click", doReload);
el.scopeSelect.addEventListener("change", doReload);
el.labelSelect.addEventListener("change", doReload);

/* ------------------------------------------------------------------ *
 * Search
 * ------------------------------------------------------------------ */

let searchDebounce = null;

function closeSearchResults() {
  el.searchResults.classList.add("hidden");
  el.searchResults.innerHTML = "";
}

function renderSearchResults(results) {
  el.searchResults.innerHTML = "";
  if (!results.length) {
    const empty = document.createElement("div");
    empty.className = "search-empty";
    empty.textContent = "no matches";
    el.searchResults.appendChild(empty);
  } else {
    for (const r of results) {
      const item = document.createElement("div");
      item.className = "search-result-item";
      const title = document.createElement("div");
      title.className = "search-result-title";
      title.textContent = r.node.title || "(untitled)";
      const meta = document.createElement("div");
      meta.className = "search-result-meta";
      meta.textContent = r.node.scope + " \u00b7 score " + r.score.toFixed(2);
      item.appendChild(title);
      item.appendChild(meta);
      item.addEventListener("click", () => selectSearchResult(r.node));
      el.searchResults.appendChild(item);
    }
  }
  el.searchResults.classList.remove("hidden");
}

async function selectSearchResult(node) {
  closeSearchResults();
  el.searchInput.value = "";
  mergeGraph({ nodes: [node], edges: [], truncated: false });
  runLayout(false);
  focusNode(node.id);
  try {
    await openEntry(node.id);
  } catch (_) {
    // surfaced already
  }
}

function focusNode(id) {
  const n = cy.getElementById(id);
  if (!n.length) return;
  cy.animate({ center: { eles: n } }, { duration: 200 });
  cy.elements().unselect();
  n.select();
}

el.searchInput.addEventListener("input", () => {
  const q = el.searchInput.value.trim();
  clearTimeout(searchDebounce);
  if (!q) {
    closeSearchResults();
    return;
  }
  searchDebounce = setTimeout(async () => {
    try {
      const scope = el.scopeSelect.value;
      const res = await api.search(q, scope);
      renderSearchResults(res.results || []);
    } catch (_) {
      // surfaced already
    }
  }, SEARCH_DEBOUNCE_MS);
});

document.addEventListener("click", (e) => {
  if (!el.searchResults.contains(e.target) && e.target !== el.searchInput) {
    closeSearchResults();
  }
});

/* ------------------------------------------------------------------ *
 * Detail panel
 * ------------------------------------------------------------------ */

let currentEntryId = null;

function closePanel() {
  el.panel.classList.add("hidden");
  currentEntryId = null;
  cy.elements().unselect();
}
el.panelClose.addEventListener("click", closePanel);

function fmtTime(t) {
  return t || "\u2014";
}

// Shared label fallback for peer/conflict refs (api/entry): title, else a
// content snippet, else "(missing)" -- reached only for a genuinely
// dangling peer, which the server already marks with title "(missing)"
// and content "". Untitled entries are the common case in real data, so
// the content snippet (not "(missing)") is what most rows actually show.
function peerLabel(peer) {
  return peer.title || (peer.content ? peer.content.slice(0, 40) : "") || "(missing)";
}

function edgeTypeBadge(type) {
  const span = document.createElement("span");
  span.className = "type-badge";
  span.textContent = type;
  span.style.color = edgeColor(type);
  return span;
}

function renderEdgeRow(edgeWithPeer, isOutgoing) {
  const row = document.createElement("div");
  row.className = "edge-row";
  row.appendChild(edgeTypeBadge(edgeWithPeer.edge.type));
  const weight = document.createElement("span");
  weight.className = "edge-weight";
  weight.textContent = edgeWithPeer.edge.weight.toFixed(2);
  row.appendChild(weight);
  const arrow = document.createElement("span");
  arrow.textContent = isOutgoing ? "\u2192" : "\u2190";
  row.appendChild(arrow);
  const title = document.createElement("span");
  title.className = "peer-title";
  title.textContent = peerLabel(edgeWithPeer.peer);
  row.appendChild(title);
  row.addEventListener("click", () => navigateToPeer(edgeWithPeer.peer.id));
  return row;
}

async function navigateToPeer(id) {
  try {
    await openEntry(id);
    focusNode(id);
  } catch (_) {
    // surfaced already
  }
}

function renderPanel(detail) {
  const entry = detail.entry;
  const node = detail.node;
  el.panelTitle.textContent = entry.title || "(untitled)";
  el.panelBody.innerHTML = "";

  const metaSection = document.createElement("div");
  metaSection.className = "panel-section";
  const dl = document.createElement("dl");
  dl.className = "meta-grid";
  const rows = [
    ["scope", entry.scope],
    ["source", (entry.source && entry.source.type) || node.source_type || "\u2014"],
    ["provenance", (entry.provenance && entry.provenance.level) || "\u2014"],
    ["observed", fmtTime((entry.freshness && entry.freshness.observed_at) || node.observed_at)],
    ["created", fmtTime(entry.created_at || node.created_at)],
    ["updated", fmtTime(entry.updated_at || node.updated_at)],
    ["version", entry.version !== undefined ? String(entry.version) : "\u2014"],
    ["labels", (entry.labels && entry.labels.length ? entry.labels.join(", ") : (node.labels || []).join(", ")) || "\u2014"],
  ];
  for (const [k, v] of rows) {
    const dt = document.createElement("dt");
    dt.textContent = k;
    const dd = document.createElement("dd");
    dd.textContent = v;
    dl.appendChild(dt);
    dl.appendChild(dd);
  }
  metaSection.appendChild(dl);
  el.panelBody.appendChild(metaSection);

  const contentSection = document.createElement("div");
  contentSection.className = "panel-section";
  const h3c = document.createElement("h3");
  h3c.textContent = "content";
  const content = document.createElement("div");
  content.className = "panel-content";
  content.textContent = entry.content || "";
  contentSection.appendChild(h3c);
  contentSection.appendChild(content);
  el.panelBody.appendChild(contentSection);

  const outSection = document.createElement("div");
  outSection.className = "panel-section";
  const h3o = document.createElement("h3");
  h3o.textContent = "edges out (" + (detail.edges_out || []).length + ")";
  outSection.appendChild(h3o);
  if (!detail.edges_out || !detail.edges_out.length) {
    const empty = document.createElement("div");
    empty.className = "empty-note";
    empty.textContent = "none";
    outSection.appendChild(empty);
  } else {
    for (const ep of detail.edges_out) outSection.appendChild(renderEdgeRow(ep, true));
  }
  el.panelBody.appendChild(outSection);

  const inSection = document.createElement("div");
  inSection.className = "panel-section";
  const h3i = document.createElement("h3");
  h3i.textContent = "edges in (" + (detail.edges_in || []).length + ")";
  inSection.appendChild(h3i);
  if (!detail.edges_in || !detail.edges_in.length) {
    const empty = document.createElement("div");
    empty.className = "empty-note";
    empty.textContent = "none";
    inSection.appendChild(empty);
  } else {
    for (const ep of detail.edges_in) inSection.appendChild(renderEdgeRow(ep, false));
  }
  el.panelBody.appendChild(inSection);

  const conflictSection = document.createElement("div");
  conflictSection.className = "panel-section";
  const h3x = document.createElement("h3");
  h3x.textContent = "conflicts (" + (detail.conflicts || []).length + ")";
  conflictSection.appendChild(h3x);
  if (!detail.conflicts || !detail.conflicts.length) {
    const empty = document.createElement("div");
    empty.className = "empty-note";
    empty.textContent = "none";
    conflictSection.appendChild(empty);
  } else {
    for (const c of detail.conflicts) {
      const row = document.createElement("div");
      row.className = "conflict-row";
      row.textContent = peerLabel(c) + " \u2014 " + c.scope;
      row.addEventListener("click", () => navigateToPeer(c.id));
      conflictSection.appendChild(row);
    }
  }
  el.panelBody.appendChild(conflictSection);

  const expandSection = document.createElement("div");
  expandSection.className = "panel-section";
  const h3e = document.createElement("h3");
  h3e.textContent = "expand";
  expandSection.appendChild(h3e);
  const controls = document.createElement("div");
  controls.className = "expand-controls";

  const dirField = document.createElement("label");
  dirField.className = "field";
  dirField.innerHTML = "<span>direction</span>";
  const dirSelect = document.createElement("select");
  for (const d of ["both", "out", "in"]) {
    const opt = document.createElement("option");
    opt.value = d;
    opt.textContent = d;
    dirSelect.appendChild(opt);
  }
  dirField.appendChild(dirSelect);

  const depthField = document.createElement("label");
  depthField.className = "field";
  depthField.innerHTML = "<span>depth</span>";
  const depthInput = document.createElement("input");
  depthInput.type = "number";
  depthInput.min = "1";
  depthInput.max = "5";
  depthInput.value = "1";
  depthField.appendChild(depthInput);

  const expandBtn = document.createElement("button");
  expandBtn.type = "button";
  expandBtn.className = "btn";
  expandBtn.textContent = "Expand";
  expandBtn.addEventListener("click", async () => {
    try {
      const g = await api.neighbors(node.id, dirSelect.value, depthInput.value);
      mergeGraph(g);
      runLayout(false);
      if (g.truncated) showNotice("Neighbor expansion truncated by server limit.");
    } catch (_) {
      // surfaced already
    }
  });

  controls.appendChild(dirField);
  controls.appendChild(depthField);
  controls.appendChild(expandBtn);
  expandSection.appendChild(controls);
  el.panelBody.appendChild(expandSection);

  el.panel.classList.remove("hidden");
}

async function openEntry(id) {
  const detail = await api.entry(id);
  currentEntryId = id;
  mergeGraph({ nodes: [detail.node], edges: [], truncated: false });
  renderPanel(detail);
}

/* ------------------------------------------------------------------ *
 * Path mode
 * ------------------------------------------------------------------ */

let pathMode = false;
let pathFrom = null; // node id
let pathResolved = false;

function clearPathHighlight() {
  cy.elements().removeClass("path-dim path-highlight path-endpoint");
}

function setPathMode(on) {
  pathMode = on;
  pathFrom = null;
  pathResolved = false;
  el.pathModeBtn.classList.toggle("active", on);
  el.pathModeBtn.setAttribute("aria-pressed", String(on));
  clearPathHighlight();
  cy.elements().unselect();
  // Cytoscape's built-in tap-to-select applies before our own tap handler
  // runs, so an after-the-fact unselect() inside the handler can't reliably
  // suppress the default yellow selection ring on the tapped node; disable
  // selection outright for the duration of path mode instead.
  cy.autounselectify(on);
  el.pathNotice.classList.add("hidden");
  closePanel();
}

el.pathModeBtn.addEventListener("click", () => setPathMode(!pathMode));

async function handlePathClick(node) {
  if (pathResolved) {
    // starting a fresh pair after a resolved path
    clearPathHighlight();
    pathFrom = null;
    pathResolved = false;
  }
  if (!pathFrom) {
    pathFrom = node.id();
    node.addClass("path-endpoint");
    return;
  }
  const to = node.id();
  if (to === pathFrom) return; // ignore re-click on same node
  try {
    const result = await api.path(pathFrom, to);
    if (!result.nodes || !result.nodes.length) {
      const fromTitle = cy.getElementById(pathFrom).data("title") || pathFrom;
      const toTitle = cy.getElementById(to).data("title") || to;
      showNotice("No path found between \u201c" + fromTitle + "\u201d and \u201c" + toTitle + "\u201d.");
      cy.getElementById(pathFrom).removeClass("path-endpoint");
      pathFrom = null;
      return;
    }
    mergeGraph(result);
    const pathNodeIds = new Set(result.nodes.map((n) => n.id));
    const pathEdgeIds = new Set(result.edges.map((e) => e.id));
    clearPathHighlight();
    cy.nodes().forEach((n) => {
      n.addClass(pathNodeIds.has(n.id()) ? "path-highlight" : "path-dim");
    });
    cy.edges().forEach((e) => {
      e.addClass(pathEdgeIds.has(e.id()) ? "path-highlight" : "path-dim");
    });
    pathResolved = true;
  } catch (_) {
    pathFrom = null;
    cy.getElementById(node.id()).removeClass("path-endpoint");
  }
}

/* ------------------------------------------------------------------ *
 * Edge popover
 * ------------------------------------------------------------------ */

function showEdgePopover(edge, renderedPosition) {
  const d = edge.data();
  el.edgePopover.innerHTML = "";
  const typeRow = document.createElement("div");
  const typeStrong = document.createElement("strong");
  typeStrong.textContent = d.type;
  typeRow.appendChild(typeStrong);
  const weightRow = document.createElement("div");
  weightRow.textContent = "weight: " + Number(d.weight).toFixed(2);
  const explicitRow = document.createElement("div");
  explicitRow.textContent = "explicit: " + (d.explicit ? "yes" : "no");
  const createdRow = document.createElement("div");
  createdRow.textContent = "created: " + (d.created_at || "\u2014");
  el.edgePopover.appendChild(typeRow);
  el.edgePopover.appendChild(weightRow);
  el.edgePopover.appendChild(explicitRow);
  el.edgePopover.appendChild(createdRow);
  const containerRect = el.cyContainer.getBoundingClientRect();
  el.edgePopover.style.left = containerRect.left + renderedPosition.x + 12 + "px";
  el.edgePopover.style.top = containerRect.top + renderedPosition.y + 12 + "px";
  el.edgePopover.classList.remove("hidden");
}
function hideEdgePopover() {
  el.edgePopover.classList.add("hidden");
}

/* ------------------------------------------------------------------ *
 * Cytoscape interaction wiring
 * ------------------------------------------------------------------ */

cy.on("tap", "node", (evt) => {
  hideEdgePopover();
  const node = evt.target;
  if (pathMode) {
    handlePathClick(node);
    return;
  }
  openEntry(node.id()).then(() => focusSelectOnly(node)).catch(() => {});
});

function focusSelectOnly(node) {
  cy.elements().unselect();
  node.select();
}

cy.on("tap", "edge", (evt) => {
  const edge = evt.target;
  showEdgePopover(edge, evt.renderedPosition || evt.position);
});

cy.on("tap", (evt) => {
  if (evt.target === cy) {
    hideEdgePopover();
  }
});

/* ------------------------------------------------------------------ *
 * Boot
 * ------------------------------------------------------------------ */

async function boot() {
  try {
    await loadMeta();
    await loadGraph();
  } catch (_) {
    // surfaced already via showError
  }
}

boot();
