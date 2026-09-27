// Retro sound effects, synthesized with the Web Audio API. The engine sends
// the names of the sounds each game raised (Update.sounds); this module turns
// each name into a short burst of oscillators and filtered noise. There are
// no audio files.

const STORE_KEY = 'sound';
const MAX_PER_UPDATE = 4;

let enabled = true;
try { enabled = localStorage.getItem(STORE_KEY) !== 'off'; } catch {}

// SYSTEM1_SOUND=off (Setup's soundOff) silences a session, such as an
// automated test run, without touching the saved preference. No
// AudioContext is created before Setup says whether sound is allowed.
let ready = false;
let forcedOff = false;

let ac = null;      // AudioContext, created only while sound is on
let out = null;     // master gain
let noiseBuf = null;

export const soundOn = () => enabled && !forcedOff;

// allowAudio is called once Setup has answered: soundOff forces sound off
// for this session only.
export function allowAudio(soundOff) {
  forcedOff = !!soundOff;
  ready = true;
}

export function setSound(on) {
  if (forcedOff) return; // muted for this session by SYSTEM1_SOUND=off
  enabled = !!on;
  try { localStorage.setItem(STORE_KEY, enabled ? 'on' : 'off'); } catch {}
  if (enabled) unlockAudio();
  else if (ac && ac.state === 'running') ac.suspend().catch(() => {});
}

// unlockAudio creates or resumes the AudioContext. WebKit keeps a context
// suspended until a user gesture, so it is also called from key and pointer
// handlers.
export function unlockAudio() {
  if (!enabled || !ready || forcedOff) return;
  if (!ac) {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return;
    try { ac = new AC(); } catch { return; }
    const comp = ac.createDynamicsCompressor();
    comp.connect(ac.destination);
    out = ac.createGain();
    out.gain.value = 0.35;
    out.connect(comp);
    noiseBuf = ac.createBuffer(1, ac.sampleRate, ac.sampleRate);
    const d = noiseBuf.getChannelData(0);
    for (let i = 0; i < d.length; i++) d[i] = Math.random() * 2 - 1;
  }
  if (ac.state === 'suspended') ac.resume().catch(() => {});
}

// --- Primitives --------------------------------------------------------------

// env shapes a gain node: a quick attack to vol, then an exponential decay.
function env(t, dur, vol, attack = 0.004) {
  const g = ac.createGain();
  g.gain.setValueAtTime(0.0001, t);
  g.gain.linearRampToValueAtTime(vol, t + attack);
  g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
  g.connect(out);
  return g;
}

// tone plays one oscillator note, optionally sliding to another pitch.
function tone(freq, dur, {type = 'square', vol = 0.15, to, delay = 0, curve = 'exp', lowpass} = {}) {
  const t = ac.currentTime + delay;
  const o = ac.createOscillator();
  o.type = type;
  o.frequency.setValueAtTime(freq, t);
  if (to) {
    if (curve === 'exp') o.frequency.exponentialRampToValueAtTime(to, t + dur);
    else o.frequency.linearRampToValueAtTime(to, t + dur);
  }
  let node = env(t, dur, vol);
  if (lowpass) {
    const f = ac.createBiquadFilter();
    f.type = 'lowpass';
    f.frequency.value = lowpass;
    f.connect(node);
    node = f;
  }
  o.connect(node);
  o.start(t);
  o.stop(t + dur + 0.02);
  return o;
}

// noise plays filtered white noise; the filter can sweep from freq to `to`.
function noise(dur, {vol = 0.2, type = 'lowpass', freq = 2000, to, q = 1, delay = 0} = {}) {
  const t = ac.currentTime + delay;
  const s = ac.createBufferSource();
  s.buffer = noiseBuf;
  const f = ac.createBiquadFilter();
  f.type = type;
  f.Q.value = q;
  f.frequency.setValueAtTime(freq, t);
  if (to) f.frequency.exponentialRampToValueAtTime(to, t + dur);
  s.connect(f);
  f.connect(env(t, dur, vol));
  s.start(t, Math.random() * 0.5);
  s.stop(t + dur + 0.02);
}

// notes plays a sequence of pitches, one every step seconds.
function notes(freqs, step, opts = {}) {
  freqs.forEach((f, i) => {
    if (f) tone(f, opts.len ?? step * 0.9, {...opts, delay: (opts.delay || 0) + i * step});
  });
}

// warble is a square tone whose pitch swings ±depth Hz around freq, rate
// times a second, for the mystery ship; with `to` it also falls to that pitch.
function warble(freq, depth, dur, rate, {vol = 0.06, to} = {}) {
  const t = ac.currentTime;
  const o = ac.createOscillator();
  o.type = 'square';
  o.frequency.setValueAtTime(freq, t);
  const lfo = ac.createOscillator();
  lfo.frequency.value = rate;
  const swing = ac.createGain();
  swing.gain.setValueAtTime(depth, t);
  if (to) {
    o.frequency.exponentialRampToValueAtTime(to, t + dur);
    swing.gain.exponentialRampToValueAtTime(Math.max(to / 4, 1), t + dur);
  }
  lfo.connect(swing);
  swing.connect(o.frequency);
  o.connect(env(t, dur, vol, 0.01));
  o.start(t); lfo.start(t);
  o.stop(t + dur + 0.02); lfo.stop(t + dur + 0.02);
}

// Note frequencies (Hz).
const C4 = 261.6, E4 = 329.6, G4 = 392.0, C5 = 523.3, E5 = 659.3, G5 = 784.0, C6 = 1046.5, E6 = 1318.5;

// --- Sounds ------------------------------------------------------------------

const SOUNDS = {
  // Shared
  gameover: () => {
    const seq = [G4, E4, C4, 196.0];
    notes(seq, 0.16, {type: 'triangle', vol: 0.3, len: 0.2});
    notes(seq, 0.16, {type: 'square', vol: 0.05, len: 0.14});
  },

  // Tetris
  move: () => tone(330, 0.03, {vol: 0.08}),
  rotate: () => tone(520, 0.05, {vol: 0.09, to: 820}),
  drop: () => { tone(220, 0.12, {type: 'triangle', vol: 0.35, to: 55}); noise(0.08, {vol: 0.12, freq: 900}); },
  lock: () => tone(150, 0.07, {type: 'triangle', vol: 0.28, to: 70}),
  line: () => { notes([C5, E5, G5], 0.05, {vol: 0.1}); noise(0.25, {vol: 0.06, type: 'bandpass', freq: 3000, to: 600, q: 2, delay: 0.1}); },
  tetris: () => { notes([C5, E5, G5, C6, E6], 0.06, {vol: 0.1}); notes([C4, E4, G4, C5, E5], 0.06, {type: 'triangle', vol: 0.2}); },
  levelup: () => notes([G4, C5, E5, G5, 0, C6], 0.07, {vol: 0.09, delay: 0.3}),

  // Frogger
  hop: () => tone(500, 0.04, {vol: 0.08, to: 900}),
  home: () => notes([E5, G5, C6], 0.07, {vol: 0.1}),
  level: () => { notes([C5, E5, G5, C6, G5, C6], 0.09, {vol: 0.1}); notes([C4, 0, G4, 0, E4, C5], 0.09, {type: 'triangle', vol: 0.2}); },
  squash: () => { noise(0.22, {vol: 0.3, type: 'bandpass', freq: 800, to: 200, q: 1.5}); tone(200, 0.2, {vol: 0.1, to: 50}); },
  splash: () => noise(0.45, {vol: 0.3, type: 'bandpass', freq: 2500, to: 350, q: 1.2}),
  timeout: () => tone(440, 0.45, {vol: 0.1, to: 110}),
  hurry: () => notes([880, 0, 880], 0.08, {vol: 0.07, len: 0.06}),

  // Space Invaders
  shoot: () => { tone(1100, 0.14, {type: 'sawtooth', vol: 0.06, to: 180, lowpass: 3000}); noise(0.05, {vol: 0.05, type: 'highpass', freq: 3000}); },
  invader: () => { noise(0.16, {vol: 0.25, type: 'bandpass', freq: 1800, to: 300, q: 1}); tone(400, 0.12, {vol: 0.06, to: 90}); },
  player_die: () => { noise(0.9, {vol: 0.35, freq: 1500, to: 80}); tone(160, 0.8, {type: 'sawtooth', vol: 0.08, to: 35, lowpass: 900}); },
  march1: () => tone(98.0, 0.09, {vol: 0.22, lowpass: 900}),
  march2: () => tone(87.3, 0.09, {vol: 0.22, lowpass: 900}),
  march3: () => tone(77.8, 0.09, {vol: 0.22, lowpass: 900}),
  march4: () => tone(73.4, 0.09, {vol: 0.22, lowpass: 900}),
  ufo: () => warble(750, 150, 0.2, 25),
  ufo_hit: () => warble(1000, 300, 0.6, 18, {vol: 0.08, to: 150}),
  wave: () => notes([C5, E5, G5, C6], 0.08, {vol: 0.1}),
};

// Most important first: when an update carries more sounds than we play,
// the rest are dropped.
const PRIORITY = ['gameover', 'player_die', 'tetris', 'level', 'levelup', 'wave', 'squash', 'splash', 'timeout',
  'line', 'home', 'ufo_hit', 'invader', 'drop', 'lock', 'shoot', 'hop', 'rotate', 'move', 'hurry', 'ufo'];
const rank = (n) => { const i = PRIORITY.indexOf(n); return i < 0 ? PRIORITY.length : i; };

// playSounds plays the sounds from one engine update. A burst (a lockstep
// step that ran many ticks) is trimmed: only the last march note and the
// most important few effects play.
export function playSounds(names, paused) {
  if (!enabled || !names || !names.length) return;
  if (!ac || ac.state !== 'running') return; // no gesture yet, or sound just turned off
  let list = paused ? names.filter((n) => n === 'gameover') : names.slice();
  const marches = list.filter((n) => n.startsWith('march'));
  if (marches.length > 1) list = list.filter((n) => !n.startsWith('march') || n === marches[marches.length - 1]);
  list.sort((a, b) => rank(a) - rank(b));
  for (const n of list.slice(0, MAX_PER_UPDATE)) {
    const fn = SOUNDS[n];
    if (fn) { try { fn(); } catch {} }
  }
}
