// End-to-end check of the diskmap UI in headless chromium over CDP.
// Usage: node e2e/e2e.mjs <diskmap-binary> <shots-dir>
// Builds a scratch fixture under ~/.cache/diskmap-e2e, drives the real UI
// (list, breadcrumb, treemap hover/click, selection, cancel, trash,
// permanent delete, partial failure) and saves screenshots. CHROME=<path>
// picks the browser binary (default /usr/bin/chromium). Items it trashes
// are removed from ~/.local/share/Trash again at the end. Exit 0 = all pass.
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const [bin, shots] = process.argv.slice(2);
if (!bin || !shots) throw new Error("usage: e2e.mjs <diskmap-binary> <shots-dir>");
fs.mkdirSync(shots, { recursive: true });

const stamp = Date.now();
const root = path.join(os.homedir(), ".cache", "diskmap-e2e", `fx-${stamp}`);
const trashFiles = path.join(os.homedir(), ".local", "share", "Trash", "files");
const trashInfo = path.join(os.homedir(), ".local", "share", "Trash", "info");
const mk = (rel, kb) => {
  const p = path.join(root, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, Buffer.alloc(kb * 1024, 7));
};
mk("videos/a.mkv", 4000); mk("videos/b.mkv", 2500); mk("videos/clips/c.mp4", 1200);
const photos = `photos-${stamp}`;
mk(`${photos}/x.jpg`, 900); mk(`${photos}/y.jpg`, 700); mk(`${photos}/z.png`, 300);
mk(`trash-me-${stamp}.bin`, 600); mk(`perm-me-${stamp}.bin`, 500);
mk("locked/stuck.bin", 400); mk(`ok-${stamp}.bin`, 200);
fs.chmodSync(path.join(root, "locked"), 0o555);

const results = [];
const check = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "PASS" : "FAIL"} ${name} ${detail}`);
};

const port = 18000 + (stamp % 1000);
const dm = spawn(bin, ["--root", root, "--port", String(port), "--no-browser"], { stdio: ["ignore", "pipe", "inherit"] });
const url = await new Promise((res) => dm.stdout.once("data", (d) => res(d.toString().trim())));
console.error(`[e2e] diskmap at ${url}`);
const chromeBin = process.env.CHROME || "/usr/bin/chromium";
const log = (m) => console.error(`[e2e] ${m}`);
const chrome = spawn(chromeBin, [
  "--headless=new", "--remote-debugging-port=0", `--user-data-dir=${path.join(os.tmpdir(), `dm-e2e-${stamp}`)}`,
  "--window-size=1600,1000", "--no-first-run", "--disable-extensions", "about:blank",
], { stdio: ["ignore", "ignore", "pipe"] });
const wsUrl = await new Promise((res) => {
  let buf = "";
  chrome.stderr.on("data", (d) => {
    buf += d;
    const m = buf.match(/DevTools listening on (ws:\S+)/);
    if (m) res(m[1]);
  });
});

log(`devtools ${wsUrl}`);
const browser = new WebSocket(wsUrl);
await new Promise((r) => (browser.onopen = r));
let id = 0;
const pending = new Map();
browser.onmessage = (ev) => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
};
const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
  const mid = ++id;
  pending.set(mid, (m) => (m.error ? rej(new Error(`${method}: ${m.error.message}`)) : res(m.result)));
  browser.send(JSON.stringify({ id: mid, method, params, sessionId }));
});
const { targetId } = await send("Target.createTarget", { url: "about:blank" });
const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
const cdp = (m, p) => send(m, p, sessionId);
await cdp("Emulation.setDeviceMetricsOverride", { width: 1600, height: 1000, deviceScaleFactor: 1, mobile: false });
await cdp("Runtime.enable");
const evalJS = async (expr) => {
  const r = await cdp("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(`${expr}: ${r.exceptionDetails.exception?.description}`);
  return r.result.value;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const waitFor = async (expr, ms = 10000) => {
  for (let t = 0; t < ms; t += 100) { if (await evalJS(expr).catch(() => false)) return true; await sleep(100); }
  return false;
};
const shot = async (name) => {
  const { data } = await cdp("Page.captureScreenshot", { format: "png" });
  fs.writeFileSync(path.join(shots, `${name}.png`), Buffer.from(data, "base64"));
};
const mouse = async (type, x, y, extra = {}) => cdp("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1, ...extra });
const click = async (x, y) => { await mouse("mousePressed", x, y); await mouse("mouseReleased", x, y); };
const clickSel = (sel) => evalJS(`document.querySelector(${JSON.stringify(sel)}).click(), true`);
const rowPaths = () => evalJS(`[...document.querySelectorAll('#rows tr[data-path]')].map(r => r.dataset.path)`);
const exists = (p) => fs.existsSync(p);

try {
  await cdp("Page.navigate", { url });
  check("C3 progress ends in finished state", await waitFor(`document.body.dataset.done === "1" && !document.getElementById("ok").hidden`));
  await waitFor(`document.querySelectorAll('#rows tr').length > 0`);
  await shot("c4-list-and-map");

  const sizes = await evalJS(`[...document.querySelectorAll('#rows tr[data-path]')].map(r => r.querySelector('.size').textContent)`);
  const paths = await rowPaths();
  const bars = await evalJS(`[...document.querySelectorAll('#rows td.bar .track > div')].map(d => d.getBoundingClientRect().width)`);
  check("C4 size bars proportional", bars[0] > 100 && Math.abs(bars[1] / bars[0] - 1949696 / 7892992) < 0.05, JSON.stringify(bars));
  check("C4 list biggest first", paths[0] === path.join(root, "videos") && paths[1] === path.join(root, photos), JSON.stringify(sizes));

  await evalJS(`document.querySelector('#rows tr[data-path="${path.join(root, "videos")}"]').click(), true`);
  await waitFor(`document.body.dataset.path === ${JSON.stringify(path.join(root, "videos"))}`);
  const vk = await rowPaths();
  check("C5 click folder shows children biggest first", vk[0].endsWith("a.mkv") && vk[1].endsWith("b.mkv") && vk[2].endsWith("clips"), JSON.stringify(vk));
  const crumbs = await evalJS(`[...document.querySelectorAll('#crumbs button')].map(b => b.dataset.path)`);
  await shot("c5-drilled");
  await evalJS(`document.querySelector('#crumbs button[data-path="${root}"]').click(), true`);
  check("C5 breadcrumb goes back up", crumbs.includes(root) && await waitFor(`document.body.dataset.path === ${JSON.stringify(root)}`));

  // Treemap: hover the photos rect, read the tooltip, click to drill in.
  const box = await evalJS(`(() => { const c = document.getElementById('map').getBoundingClientRect();
    const r = window.diskmapState.rects.find(r => r.node && r.node.path === ${JSON.stringify(path.join(root, photos))});
    return { x: c.left + r.x + r.w / 2, y: c.top + r.y + 8, area: r.w * r.h }; })()`);
  await mouse("mouseMoved", box.x, box.y);
  await sleep(150);
  const tip = await evalJS(`document.getElementById('tip').hidden ? '' : document.getElementById('tip').textContent`);
  check("C6 hover shows path and size", tip.includes(path.join(root, photos)) && /MiB|KiB/.test(tip), tip);
  await shot("c6-treemap-hover");
  const areas = await evalJS(`window.diskmapState.rects.filter(r => r.depth === 0 && r.node).map(r => [r.node.size, r.w * r.h])`);
  const ratio = areas.map(([s, a]) => a / s);
  check("C6 rect area proportional to size", Math.max(...ratio) / Math.min(...ratio) < 1.02, JSON.stringify(areas));
  await click(box.x, box.y);
  check("C6 click drills into folder", await waitFor(`document.body.dataset.path === ${JSON.stringify(path.join(root, photos))}`));
  await evalJS(`document.querySelector('#crumbs button[data-path="${root}"]').click(), true`);
  await waitFor(`document.body.dataset.path === ${JSON.stringify(root)}`);

  // Selection + cancel.
  const tName = path.join(root, `trash-me-${stamp}.bin`);
  const okName = path.join(root, `ok-${stamp}.bin`);
  const sel = (p) => evalJS(`document.querySelector('#rows tr[data-path="${p}"] input').click(), true`);
  await sel(tName); await sel(path.join(root, photos));
  const selInfo = await evalJS(`[document.getElementById('sel').hidden, document.getElementById('selcount').textContent, document.getElementById('selsize').textContent]`);
  check("C7 selection shows total and delete button", !selInfo[0] && selInfo[1] === "2" && /^2\.\d+ MiB$/.test(selInfo[2]), JSON.stringify(selInfo));
  await shot("c7-selection");
  await clickSel("#del");
  const listed = await evalJS(`[document.getElementById('confirm').open, document.querySelectorAll('#clist li').length, document.getElementById('ctotal').textContent]`);
  await shot("c8-confirm");
  await clickSel("#cancel");
  check("C8 confirm lists paths + total; cancel removes nothing",
    listed[0] && listed[1] === 2 && listed[2] === selInfo[2] && exists(tName) && exists(path.join(root, photos, "x.jpg")), JSON.stringify(listed));

  // Trash.
  await clickSel("#del");
  await clickSel("#dotrash");
  await waitFor(`document.getElementById('results').open`);
  await shot("c9-trash-results");
  await clickSel("#rclose");
  await waitFor(`!document.querySelector('#rows tr[data-path="${tName}"]')`);
  const gone = !(await rowPaths()).includes(tName) && !(await rowPaths()).includes(path.join(root, photos));
  const inTrash = exists(path.join(trashFiles, path.basename(tName))) && fs.existsSync(path.join(trashInfo, `${path.basename(tName)}.trashinfo`))
    && fs.readFileSync(path.join(trashInfo, `${path.basename(tName)}.trashinfo`), "utf8").includes(`Path=${tName}`);
  check("C9 trash: gone from list, in ~/.local/share/Trash with trashinfo", gone && !exists(tName) && inTrash);

  // Permanent: first press arms, second deletes.
  const pName = path.join(root, `perm-me-${stamp}.bin`);
  await sel(pName);
  await clickSel("#del");
  await clickSel("#perm");
  const armed = await evalJS(`document.getElementById('confirm').classList.contains('armed') && document.getElementById('confirm').open`);
  await shot("c10-perm-armed");
  const stillThere = exists(pName);
  await clickSel("#perm");
  await waitFor(`document.getElementById('results').open`);
  await clickSel("#rclose");
  check("C10 permanent needs second press; gone and not in trash", armed && stillThere && !exists(pName) && !exists(path.join(trashFiles, path.basename(pName))));

  // Partial failure.
  await evalJS(`document.querySelector('#rows tr[data-path="${path.join(root, "locked")}"]').click(), true`);
  await waitFor(`document.body.dataset.path === ${JSON.stringify(path.join(root, "locked"))}`);
  await sel(path.join(root, "locked", "stuck.bin"));
  await evalJS(`document.querySelector('#crumbs button[data-path="${root}"]').click(), true`);
  await waitFor(`document.body.dataset.path === ${JSON.stringify(root)}`);
  await sel(okName);
  await clickSel("#del");
  await clickSel("#dotrash");
  await waitFor(`document.getElementById('results').open`);
  const rs = await evalJS(`[...document.querySelectorAll('#rlist li')].map(l => l.dataset.ok + ' ' + l.textContent)`);
  await shot("c11-partial-failure");
  check("C11 failing item reported, other item handled",
    rs.some((r) => r.startsWith("0") && r.includes("stuck.bin")) && rs.some((r) => r.startsWith("1") && r.includes(path.basename(okName)))
    && exists(path.join(root, "locked", "stuck.bin")) && !exists(okName), JSON.stringify(rs));
  await clickSel("#rclose");

  const errs = await evalJS(`document.body.innerText.includes('[object Object]') || document.body.innerText.includes('undefined')`);
  check("no [object Object]/undefined in page", !errs);
} finally {
  // Remove only trash entries whose trashinfo points into this fixture.
  for (const info of fs.existsSync(trashInfo) ? fs.readdirSync(trashInfo) : []) {
    const body = fs.readFileSync(path.join(trashInfo, info), "utf8");
    if (!body.includes(`Path=${root}/`)) continue;
    fs.rmSync(path.join(trashFiles, info.replace(/\.trashinfo$/, "")), { recursive: true, force: true });
    fs.rmSync(path.join(trashInfo, info), { force: true });
  }
  fs.chmodSync(path.join(root, "locked"), 0o755);
  fs.rmSync(root, { recursive: true, force: true });
  browser.close();
  chrome.kill();
  dm.kill();
}
const failed = results.filter((r) => !r.ok).length;
console.log(`${results.length - failed}/${results.length} passed`);
process.exit(failed ? 1 : 0);
