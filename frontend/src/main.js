import './style.css';
import {Setup, Load, Reset, TogglePause, SetMode, KeyDown, KeyUp, ReleaseAll, StartAgent, StopAgent, AgentStatus, Play, GetSettings, SaveSettings, TestAgent} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';

const $ = (id) => document.getElementById(id);
const canvas = $('screen');
const ctx = canvas.getContext('2d');

let games = [];
let info = null;      // current game.Info
let latest = null;    // latest engine Update
let spriteCache = new Map();

// Keyboard -> virtual controller buttons.
const KEYS = {
  ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down',
  KeyA: 'left', KeyD: 'right', KeyW: 'up', KeyS: 'down',
  Space: 'a', KeyX: 'a', KeyZ: 'b', Enter: 'start',
};

const overlayOpen = () => !$('launch').hidden || !$('settings').hidden;

window.addEventListener('keydown', (e) => {
  if (!$('settings').hidden) {
    if (e.code === 'Escape') closeSettings();
    return;
  }
  if (!$('launch').hidden) {
    if (e.code === 'Enter') { e.preventDefault(); launch(); }
    return;
  }
  if (e.code === 'KeyP') { TogglePause(); return; }
  if (e.code === 'KeyR') { Reset(); return; }
  const b = KEYS[e.code];
  if (!b) return;
  e.preventDefault();
  if (!e.repeat) KeyDown(b);
});
window.addEventListener('keyup', (e) => {
  if (overlayOpen()) return;
  const b = KEYS[e.code];
  if (b) { e.preventDefault(); KeyUp(b); }
});
window.addEventListener('blur', () => ReleaseAll());

function selectGame(id) {
  info = games.find((g) => g.id === id);
  spriteCache = new Map();
  $('controls').textContent = info.controls;
  for (const btn of $('games').children) btn.classList.toggle('on', btn.dataset.id === id);
  for (const btn of $('launch-games').children) btn.classList.toggle('on', btn.dataset.id === id);
  resize();
}

async function init() {
  // Listen before Setup, which asks the engine for a frame of the paused game.
  EventsOn('update', (u) => {
    if (games.length && (!info || u.game !== info.id)) selectGame(u.game);
    latest = u;
  });
  const s = await Setup();
  games = s.games;
  $('games').replaceChildren(...games.map((g) => {
    const b = document.createElement('button');
    b.textContent = g.title;
    b.dataset.id = g.id;
    b.onclick = () => { Load(g.id); b.blur(); };
    return b;
  }));
  $('launch-games').replaceChildren(...games.map((g) => {
    const b = document.createElement('button');
    b.textContent = g.title;
    b.dataset.id = g.id;
    b.onclick = () => { Load(g.id); selectGame(g.id); };
    return b;
  }));
  selectGame(s.current);
  for (const b of $('launch-who').children) b.onclick = () => chooseWho(b.dataset.who);
  chooseWho(localStorage.getItem('who') || 'agent');
  $('launch-start').onclick = launch;
  $('launch-settings').onclick = openSettings;
  $('open-settings').onclick = openSettings;
  $('settings-cancel').onclick = closeSettings;
  $('settings-test').onclick = testSettings;
  $('settings-form').onsubmit = saveSettingsForm;
  for (const r of document.querySelectorAll('input[name=agent]')) r.onchange = syncSettingsForm;
  $('settings-api').textContent = s.apiAddr ? `http://${s.apiAddr}/v1` : 'disabled';
  settings = await GetSettings();
  showKind();

  const addr = $('api-addr');
  if (s.apiAddr) addr.textContent = `http://${s.apiAddr}/v1`;
  else { addr.textContent = `disabled: ${s.apiErr}`; addr.classList.add('err'); }

  for (const b of $('mode').children) b.onclick = () => { SetMode(b.dataset.mode); b.blur(); };
  $('pause').onclick = (e) => { TogglePause(); e.currentTarget.blur(); };
  $('reset').onclick = (e) => { Reset(); e.currentTarget.blur(); };
  $('agent-toggle').onclick = (e) => {
    e.currentTarget.blur();
    toggleAgent(agentState === 'off' || agentState === 'error');
  };
  setInterval(async () => showAgent(await AgentStatus()), 500);
  const st = await AgentStatus();
  showAgent(st);
  if (st.state !== 'off') $('launch').hidden = true; // started by SYSTEM1_AUTOSTART

  window.addEventListener('resize', resize);
  requestAnimationFrame(draw);
}

let settings = {agent: 'builtin'};
const kindName = () => settings.agent === "custom" ? "Custom agent" : "Built-in (Laya)";
function showKind() { $('launch-kind').textContent = kindName(); }

let who = 'agent';
function chooseWho(w) {
  who = w === 'you' ? 'you' : 'agent';
  try { localStorage.setItem('who', who); } catch {}
  for (const b of $('launch-who').children) b.classList.toggle('on', b.dataset.who === who);
}

function launch() {
  $('launch').hidden = true;
  if (who === 'agent') toggleAgent(true);
  else Play();
}

async function toggleAgent(on) {
  showAgent(await (on ? StartAgent() : StopAgent()));
}

function openSettings() {
  const f = $('settings-form');
  f.agent.value = settings.agent || 'builtin';
  $('set-url').value = settings.url || '';
  $('set-key').value = settings.apiKey || '';
  $('set-model').value = settings.model || '';
  $('set-batch').checked = !!settings.batch;
  $('set-rate').value = String(settings.inputRate ?? 6);
  $('settings-msg').textContent = '';
  syncSettingsForm();
  $('settings').hidden = false;
}
function closeSettings() { $('settings').hidden = true; }
function syncSettingsForm() { $('custom-fields').disabled = $('settings-form').agent.value !== 'custom'; }
function readSettingsForm() {
  return {agent: $('settings-form').agent.value, url: $('set-url').value.trim(), apiKey: $('set-key').value, model: $('set-model').value.trim(), batch: $('set-batch').checked, inputRate: Number($('set-rate').value)};
}
function settingsMsg(text, cls) {
  const el = $('settings-msg');
  el.textContent = text;
  el.className = `hint ${cls || ''}`;
}
async function testSettings() {
  settingsMsg('Testing…');
  try { settingsMsg(await TestAgent(readSettingsForm()), 'ok'); } catch (err) { settingsMsg(String(err), 'err'); }
}
async function saveSettingsForm(e) {
  e.preventDefault();
  const s = readSettingsForm();
  if (s.agent === 'custom' && !/^https?:\/\//.test(s.url)) { settingsMsg('Enter an http:// or https:// URL.', 'err'); return; }
  try {
    await SaveSettings(s);
    settings = s;
    showKind();
    closeSettings();
    if (agentState === 'starting' || agentState === 'running') toggleAgent(true); // restart with the new agent
  } catch (err) { settingsMsg(String(err), 'err'); }
}

let agentState = 'off';
function showAgent(st) {
  agentState = st.state;
  const btn = $('agent-toggle');
  const on = st.state === 'starting' || st.state === 'running';
  btn.textContent = on ? 'Stop agent' : 'Start agent';
  btn.classList.toggle('on', on);
  const who = st.kind === 'custom' ? 'The custom agent' : 'Laya';
  const msg = {
    off: `Off. ${kindName()} is ready to start; the game is under keyboard control.`,
    starting: `${st.detail || 'Starting…'} The game is paused until the agent answers.`,
    running: `${who} is playing the game on screen, ${settings.inputRate ? `at up to ${settings.inputRate} inputs per second` : 'at full speed'}.`,
    error: `Agent problem: ${st.detail}`,
  }[st.state] || st.state;
  const el = $('agent-status');
  el.textContent = msg;
  el.classList.toggle('err', st.state === 'error');
}

function resize() {
  if (!info) return;
  const stage = $('stage');
  const pad = 48;
  const scale = Math.max(1, Math.min((stage.clientWidth - pad) / info.width, (stage.clientHeight - pad) / info.height));
  const dpr = window.devicePixelRatio || 1;
  canvas.style.width = `${Math.floor(info.width * scale)}px`;
  canvas.style.height = `${Math.floor(info.height * scale)}px`;
  canvas.width = Math.floor(info.width * scale * dpr);
  canvas.height = Math.floor(info.height * scale * dpr);
  canvas.dataset.scale = scale * dpr;
}

function sprite(id, color) {
  const key = id + color;
  let c = spriteCache.get(key);
  if (c) return c;
  const rows = info.sprites?.[id];
  if (!rows) return null;
  c = document.createElement('canvas');
  c.width = rows[0].length;
  c.height = rows.length;
  const sctx = c.getContext('2d');
  sctx.fillStyle = color;
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] !== '.') sctx.fillRect(x, y, 1, 1);
  });
  spriteCache.set(key, c);
  return c;
}

function draw() {
  requestAnimationFrame(draw);
  if (!latest || !info) return;
  const f = latest.frame;
  const s = Number(canvas.dataset.scale) || 1;
  ctx.setTransform(s, 0, 0, s, 0, 0);
  ctx.imageSmoothingEnabled = false;
  ctx.fillStyle = f.bg;
  ctx.fillRect(0, 0, f.w, f.h);
  for (const r of f.rects || []) {
    ctx.fillStyle = r.c;
    ctx.fillRect(r.x, r.y, r.w, r.h);
  }
  for (const sp of f.sprites || []) {
    const img = sprite(sp.id, sp.c);
    if (!img) continue;
    const k = sp.s || 1;
    ctx.drawImage(img, sp.x, sp.y, img.width * k, img.height * k);
  }
  for (const t of f.texts || []) {
    ctx.fillStyle = t.c;
    ctx.font = `bold ${t.size}px ui-monospace, Menlo, Consolas, monospace`;
    ctx.textAlign = t.align || 'left';
    ctx.fillText(t.s, t.x, t.y);
  }
  renderChrome(latest);
}

let lastChrome = '';
function renderChrome(u) {
  const key = JSON.stringify([agentState, u.mode, u.paused, u.agent, u.status, Math.floor(u.tick / 15)]);
  if (key === lastChrome) return;
  lastChrome = key;

  for (const b of $('mode').children) b.classList.toggle('on', b.dataset.mode === u.mode);
  $('pause').firstChild.textContent = u.paused ? 'Resume ' : 'Pause ';
  $('mode-hint').innerHTML = u.mode === 'lockstep'
    ? 'Game advances only when the agent decides; thinking time is free.'
    : 'Game runs at 60 ticks/s; agent latency costs time.';

  const banner = $('banner');
  const msg = agentState === 'starting' ? 'Paused · starting the agent'
    : u.paused ? 'Paused' : u.mode === 'lockstep' ? 'Lockstep · waiting for agent' : '';
  banner.hidden = !msg;
  banner.textContent = msg;

  $('score').textContent = `score ${u.status.score}`;
  $('tick').textContent = `tick ${u.tick}`;

  $('sees').hidden = !u.situation;
  $('situation').textContent = u.situation || '';

  const a = u.agent;
  const live = a && a.age_ms < 1500;
  $('agent-dot').classList.toggle('live', !!live);
  if (!a) return;
  const probs = a.meta?.probabilities;
  const answers = a.meta?.answers;
  let bars = '';
  if (answers && typeof answers === 'object') {
    bars = Object.entries(answers).sort(([x], [y]) => x.localeCompare(y)).map(([q, ans]) => {
      const choice = ans.type === 'choice';
      const p = choice ? (ans.probabilities?.[ans.choice] ?? 0) : (ans.noul ?? 0);
      const label = choice ? `${esc(ans.choice)} ${(p * 100).toFixed(0)}%` : (p > 0.5 ? `yes ${(p * 100).toFixed(0)}%` : `no ${((1 - p) * 100).toFixed(0)}%`);
      return `
      <div class="bar${!choice && p > 0.5 ? ' top' : ''}">
        <span>${esc(q)}</span>
        <div class="track"><div class="fill" style="width:${(p * 100).toFixed(1)}%"></div></div>
        <span class="val">${label}</span>
      </div>`;
    }).join('');
  } else if (probs && typeof probs === 'object') {
    const entries = Object.entries(probs).sort((x, y) => y[1] - x[1]);
    bars = entries.map(([name, p], i) => `
      <div class="bar${i === 0 ? ' top' : ''}">
        <span>${esc(name)}</span>
        <div class="track"><div class="fill" style="width:${(p * 100).toFixed(1)}%"></div></div>
        <span class="p">${(p * 100).toFixed(0)}%</span>
      </div>`).join('');
  }
  const latency = a.meta?.latency_ms != null ? `<span>latency <b>${Math.round(a.meta.latency_ms)}ms</b></span>` : '';
  $('agent-body').innerHTML = `
    <div class="action">${esc(a.action)}</div>
    ${a.note ? `<div class="note">${esc(a.note)}</div>` : ''}
    <div class="stats"><span><b>${a.rate.toFixed(1)}</b>/s</span><span><b>${a.total}</b> total</span>${latency}</div>
    ${bars}`;
}

function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;'}[c]));
}

init();
