// diskmap UI: biggest-first list + squarified treemap + delete flow.
// Plain ES module, no build step: the Go binary embeds this file as is.

const TOKEN = window.DISKMAP_TOKEN;
const $ = (id) => document.getElementById(id);
const PALETTE = ["#C57293", "#398FC0", "#C85A32", "#228736", "#8D58BB", "#686D2C"];

const state = (window.diskmapState = {
  path: decodeURIComponent(location.hash.slice(1)),
  sel: new Map(), // path -> size
  failed: new Set(),
  map: null, // last treemap payload
  rects: [], // drawn rects, deepest last
  hover: null,
});

async function api(url, opts = {}) {
  const res = await fetch(url, { ...opts, headers: { "X-Token": TOKEN, "Content-Type": "application/json" } });
  if (!res.ok) throw new Error(`${res.status}`);
  return res.json();
}

export function fmtBytes(n) {
  const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : n < 10 ? 2 : 1)} ${u[i]}`;
}
const fmtNum = (n) => n.toLocaleString();

// ---- progress -------------------------------------------------------------

async function pollProgress() {
  for (;;) {
    const { progress: p, roots } = await api("/api/progress");
    $("pfiles").textContent = fmtNum(p.files);
    $("pbytes").textContent = fmtBytes(p.bytes);
    $("ptime").textContent = `${(p.ms / 1000).toFixed(1)} s`;
    if (p.done) {
      $("spin").hidden = true;
      $("ok").hidden = false;
      document.body.dataset.done = "1";
      return roots;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
}

// ---- navigation -----------------------------------------------------------

function go(path) {
  state.path = path;
  history.replaceState(null, "", `#${encodeURIComponent(path)}`);
  render();
}

function parentOf(path, roots) {
  if (roots.includes(path)) return roots.length > 1 ? "" : path;
  const i = path.lastIndexOf("/");
  return i <= 0 ? "/" : path.slice(0, i);
}

function crumbs(path, roots) {
  const nav = $("crumbs");
  nav.replaceChildren();
  const root = roots.filter((r) => path === r || path.startsWith(r === "/" ? "/" : `${r}/`))
    .sort((a, b) => b.length - a.length)[0];
  const parts = [];
  if (roots.length > 1) parts.push(["⌂", ""]);
  if (root !== undefined) {
    parts.push([root, root]);
    let acc = root;
    for (const seg of path.slice(root.length).split("/").filter(Boolean)) {
      acc = acc === "/" ? `/${seg}` : `${acc}/${seg}`;
      parts.push([seg, acc]);
    }
  }
  parts.forEach(([label, target], i) => {
    if (i > 0) {
      const s = document.createElement("span");
      s.className = "sep";
      s.textContent = "›";
      s.setAttribute("aria-hidden", "true");
      nav.append(s);
    }
    const b = document.createElement("button");
    b.textContent = label;
    b.dataset.path = target;
    b.onclick = () => go(target);
    nav.append(b);
  });
}

// ---- list -----------------------------------------------------------------

const ICON_DIR = '<svg class="dir" viewBox="0 0 24 24"><path d="M10 4H4a2 2 0 00-2 2v12a2 2 0 002 2h16a2 2 0 002-2V8a2 2 0 00-2-2h-8z"/></svg>';
const ICON_FILE = '<svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8zm-1 7V3.5L18.5 9z"/></svg>';
const ICON_LOCK = '<svg viewBox="0 0 24 24" aria-label="Unreadable"><path d="M18 8h-1V6A5 5 0 007 6v2H6a2 2 0 00-2 2v10a2 2 0 002 2h12a2 2 0 002-2V10a2 2 0 00-2-2zM9 6a3 3 0 016 0v2H9z"/></svg>';

function renderList(node) {
  const tbody = $("rows");
  tbody.replaceChildren();
  const max = node.kids?.[0]?.size || 1;
  for (const k of node.kids || []) {
    const tr = document.createElement("tr");
    tr.dataset.path = k.path;
    if (state.sel.has(k.path)) tr.classList.add("selected");
    if (state.failed.has(k.path)) tr.classList.add("failed");
    const chk = document.createElement("input");
    chk.type = "checkbox";
    chk.checked = state.sel.has(k.path);
    chk.setAttribute("aria-label", `Select ${k.path}`);
    chk.onclick = (ev) => { ev.stopPropagation(); toggleSel(k); };
    const cells = [
      ["chk", chk],
      ["size", fmtBytes(k.size)],
      ["bar", `<div class="track"><div style="width:${Math.max(0.5, (100 * k.size) / max)}%"></div></div>`],
      ["name", (k.dir ? ICON_DIR : ICON_FILE) + (k.err ? ICON_LOCK : "")],
      ["count", k.dir ? fmtNum(k.count) : ""],
    ];
    for (const [cls, content] of cells) {
      const td = document.createElement("td");
      td.className = cls;
      if (content instanceof Node) td.append(content);
      else if (cls === "size" || cls === "count") td.textContent = content;
      else td.innerHTML = content;
      if (cls === "name") td.append(k.name);
      tr.append(td);
    }
    tr.onclick = () => (k.dir && !k.mount ? go(k.path) : toggleSel(k));
    tbody.append(tr);
  }
  if (node.more) {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.colSpan = 5;
    td.className = "more";
    td.textContent = `+${fmtNum(node.more)} · ${fmtBytes(node.moreSize)}`;
    tr.append(td);
    tbody.append(tr);
  }
}

// ---- selection ------------------------------------------------------------

function toggleSel(e) {
  if (state.sel.has(e.path)) state.sel.delete(e.path);
  else state.sel.set(e.path, e.size);
  syncSel();
}

function syncSel() {
  // An item inside a selected folder is already covered; count it once.
  const paths = [...state.sel.keys()];
  let total = 0;
  for (const p of paths) {
    if (!paths.some((q) => q !== p && p.startsWith(q === "/" ? "/" : `${q}/`))) total += state.sel.get(p);
  }
  $("sel").hidden = state.sel.size === 0;
  $("selcount").textContent = fmtNum(state.sel.size);
  $("selsize").textContent = fmtBytes(total);
  for (const tr of $("rows").children) {
    const on = state.sel.has(tr.dataset.path);
    tr.classList.toggle("selected", on);
    const c = tr.querySelector("input");
    if (c) c.checked = on;
  }
  drawMap();
  return total;
}

// ---- treemap --------------------------------------------------------------

// squarify lays out sizes (desc) in rect, returning [x,y,w,h] per item.
export function squarify(sizes, x, y, w, h) {
  const out = [];
  const total = sizes.reduce((a, b) => a + b, 0);
  if (total <= 0 || w <= 0 || h <= 0) return sizes.map(() => [x, y, 0, 0]);
  const scale = (w * h) / total;
  const areas = sizes.map((s) => s * scale);
  let i = 0;
  while (i < areas.length) {
    const side = Math.min(w, h);
    let row = [areas[i]];
    let j = i + 1;
    const worst = (r) => {
      const s = r.reduce((a, b) => a + b, 0);
      const mx = Math.max(...r), mn = Math.min(...r);
      return Math.max((side * side * mx) / (s * s), (s * s) / (side * side * mn));
    };
    while (j < areas.length && worst([...row, areas[j]]) <= worst(row)) row.push(areas[j++]);
    const sum = row.reduce((a, b) => a + b, 0);
    const thick = sum / side;
    let off = 0;
    for (const a of row) {
      const len = a / thick;
      out.push(w >= h ? [x, y + off, thick, len] : [x + off, y, len, thick]);
      off += len;
    }
    if (w >= h) { x += thick; w -= thick; } else { y += thick; h -= thick; }
    i = j;
  }
  return out;
}

function colorFor(name) {
  const dot = name.lastIndexOf(".");
  const ext = dot > 0 ? name.slice(dot + 1) : name;
  let h = 0;
  for (const c of ext) h = (h * 31 + c.charCodeAt(0)) | 0;
  return PALETTE[Math.abs(h) % PALETTE.length];
}

function layout(node, x, y, w, h, depth, rects) {
  const kids = (node.kids || []).filter((k) => k.size > 0);
  const items = kids.map((k) => ({ node: k, size: k.size }));
  if (node.more && node.moreSize > 0) items.push({ node: null, size: node.moreSize });
  const boxes = squarify(items.map((i) => i.size), x, y, w, h);
  items.forEach((it, i) => {
    const [bx, by, bw, bh] = boxes[i];
    if (bw < 1 || bh < 1) return;
    const r = { x: bx, y: by, w: bw, h: bh, node: it.node, depth };
    rects.push(r);
    const k = it.node;
    if (k && k.dir && k.kids && bw > 40 && bh > 40) {
      layout(k, bx + 3, by + 20, bw - 6, bh - 23, depth + 1, rects);
    }
  });
}

function drawMap() {
  const canvas = $("map");
  const dpr = window.devicePixelRatio || 1;
  const { width, height } = canvas.getBoundingClientRect();
  canvas.width = Math.round(width * dpr);
  canvas.height = Math.round(height * dpr);
  const ctx = canvas.getContext("2d");
  ctx.scale(dpr, dpr);
  ctx.fillStyle = "#211D1B";
  ctx.fillRect(0, 0, width, height);
  if (!state.map) return;
  state.rects = [];
  layout(state.map, 0, 0, width, height, 0, state.rects);
  ctx.font = "13px system-ui, sans-serif";
  ctx.textBaseline = "top";
  for (const r of state.rects) {
    const k = r.node;
    ctx.fillStyle = !k ? "#463E3A" : k.dir ? (r.depth % 2 ? "#38312E" : "#2B2624") : colorFor(k.name);
    ctx.fillRect(r.x, r.y, r.w, r.h);
    ctx.strokeStyle = "#211D1B";
    ctx.lineWidth = 1;
    ctx.strokeRect(r.x + 0.5, r.y + 0.5, r.w - 1, r.h - 1);
    if (k && state.sel.has(k.path)) {
      ctx.strokeStyle = "#B8862E";
      ctx.lineWidth = 3;
      ctx.strokeRect(r.x + 1.5, r.y + 1.5, r.w - 3, r.h - 3);
    }
    if (k && r.w > 50 && r.h > 18) {
      ctx.save();
      ctx.beginPath();
      ctx.rect(r.x, r.y, r.w, r.h);
      ctx.clip();
      ctx.fillStyle = k.dir ? "#ECEAE9" : "#211D1B";
      ctx.fillText(k.name, r.x + 4, r.y + 4);
      ctx.restore();
    }
  }
  if (state.hover) {
    const r = state.hover;
    ctx.strokeStyle = "#ECEAE9";
    ctx.lineWidth = 2;
    ctx.strokeRect(r.x + 1, r.y + 1, r.w - 2, r.h - 2);
  }
}

function hit(ev) {
  const b = $("map").getBoundingClientRect();
  const px = ev.clientX - b.left, py = ev.clientY - b.top;
  for (let i = state.rects.length - 1; i >= 0; i--) {
    const r = state.rects[i];
    if (px >= r.x && px < r.x + r.w && py >= r.y && py < r.y + r.h) return { r, px, py };
  }
  return null;
}

function setupMap() {
  const canvas = $("map");
  const tip = $("tip");
  canvas.addEventListener("mousemove", (ev) => {
    const h = hit(ev);
    const r = h?.r ?? null;
    if (r !== state.hover) { state.hover = r; drawMap(); }
    if (!h) { tip.hidden = true; return; }
    const k = r.node;
    tip.textContent = k ? `${k.path}  ·  ${fmtBytes(k.size)}` : `+${fmtNum(state.map.more)} · ${fmtBytes(state.map.moreSize)}`;
    tip.hidden = false;
    const wrap = $("mapwrap").getBoundingClientRect();
    tip.style.left = `${Math.min(h.px + 14, wrap.width - tip.offsetWidth - 4)}px`;
    tip.style.top = `${Math.min(h.py + 14, wrap.height - tip.offsetHeight - 4)}px`;
  });
  canvas.addEventListener("mouseleave", () => { tip.hidden = true; state.hover = null; drawMap(); });
  canvas.addEventListener("click", (ev) => {
    const k = hit(ev)?.r.node;
    if (!k) return;
    if (ev.ctrlKey || ev.metaKey || !k.dir || k.mount) toggleSel(k);
    else go(k.path);
  });
  new ResizeObserver(drawMap).observe($("mapwrap"));
}

// ---- delete flow ----------------------------------------------------------

function topLevelSelection() {
  const paths = [...state.sel.keys()];
  return paths.filter((p) => !paths.some((q) => q !== p && p.startsWith(q === "/" ? "/" : `${q}/`)));
}

function openConfirm() {
  const dlg = $("confirm");
  dlg.classList.remove("armed");
  const ul = $("clist");
  ul.replaceChildren();
  let total = 0;
  for (const p of topLevelSelection()) {
    const li = document.createElement("li");
    const sz = document.createElement("span");
    sz.className = "sz";
    sz.textContent = fmtBytes(state.sel.get(p));
    total += state.sel.get(p);
    li.append(p, sz);
    ul.append(li);
  }
  $("ctotal").textContent = fmtBytes(total);
  dlg.showModal();
}

async function doDelete(permanent) {
  const paths = topLevelSelection();
  $("confirm").close();
  const res = await api("/api/delete", { method: "POST", body: JSON.stringify({ paths, permanent }) });
  const ul = $("rlist");
  ul.replaceChildren();
  state.failed.clear();
  for (const r of res) {
    const li = document.createElement("li");
    li.className = r.ok ? "good" : "bad";
    li.dataset.ok = r.ok ? "1" : "0";
    li.append(r.ok ? "✓ " : "⚠ ", r.path);
    if (!r.ok) {
      const e = document.createElement("span");
      e.className = "sz";
      e.textContent = r.error;
      li.append(e);
      state.failed.add(r.path);
    } else {
      state.sel.delete(r.path);
    }
    ul.append(li);
  }
  for (const p of [...state.sel.keys()]) if (!state.failed.has(p)) state.sel.delete(p);
  syncSel();
  const dlg = $("results");
  dlg.showModal();
  if (state.failed.size) { dlg.classList.add("shake"); setTimeout(() => dlg.classList.remove("shake"), 400); }
  await render();
}

function setupDelete() {
  $("del").onclick = openConfirm;
  $("clear").onclick = () => { state.sel.clear(); syncSel(); };
  $("cancel").onclick = () => $("confirm").close();
  $("dotrash").onclick = () => doDelete(false);
  $("perm").onclick = () => {
    const dlg = $("confirm");
    if (!dlg.classList.contains("armed")) { dlg.classList.add("armed"); return; } // second confirm
    doDelete(true);
  };
  $("rclose").onclick = () => $("results").close();
}

// ---- main -----------------------------------------------------------------

let roots = [];

async function render() {
  const [list, map] = await Promise.all([
    api(`/api/tree?depth=1&limit=1000&path=${encodeURIComponent(state.path)}`),
    api(`/api/tree?depth=3&limit=120&path=${encodeURIComponent(state.path)}`),
  ]).catch(() => [null, null]);
  if (!list) { go(roots.length === 1 ? roots[0] : ""); return; }
  crumbs(state.path, roots);
  $("up").disabled = state.path === "" || (roots.length === 1 && state.path === roots[0]);
  renderList(list);
  state.map = map;
  state.hover = null;
  drawMap();
  document.body.dataset.path = state.path;
}

async function main() {
  setupMap();
  setupDelete();
  $("up").onclick = () => go(parentOf(state.path, roots));
  roots = await pollProgress();
  if (!state.path) state.path = roots.length === 1 ? roots[0] : "";
  await render();
}

if (TOKEN) main();
