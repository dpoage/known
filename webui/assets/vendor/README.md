# Vendored dependencies

## cytoscape.min.js

- Version: **3.30.4** (pinned; verify via `cytoscape.version` at runtime).
- Source: https://unpkg.com/cytoscape@3.30.4/dist/cytoscape.min.js
- License: MIT, Copyright (c) 2016-2024 The Cytoscape Consortium. Full license
  text is retained verbatim in the header comment of the vendored file.
- Format: UMD build; loaded as a classic `<script>` tag, exposes the global
  `window.cytoscape`. No build step, no CDN reference, no npm/node artifacts
  required at runtime.
- To upgrade: replace this file with a newer `cytoscape@<version>/dist/cytoscape.min.js`
  and bump the version noted here and in `index.html`'s script comment.
