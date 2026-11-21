// Shared palette and drawing helpers for every scene.
// The look: black ground, amber as the default, cyan-green for matched,
// red for wrong, white for the thing to look at. Uppercase monospace, square corners.

export const C = {
  bg: '#000000', cell: '#17110a', pend: '#fb8b1e', match: '#4af6c3', matchDim: '#0e4a3a',
  flash: '#ffffff', fp: '#ff433d', ink: '#ffa028', hi: '#ffffff', mute: '#a8701c', line: '#3a2a10',
  card: '#0a0a0a', nm: '#3d8bff', miss: '#ff5fd2',
  amount: '#3d8bff', time: '#ff5fd2', account: '#ffe14d', text: '#4af6c3',
};

export const FONT = '"Tally Mono", "JetBrains Mono", Menlo, monospace';

export const clamp = (v) => (v < 0 ? 0 : v > 1 ? 1 : v);
export const prog = (t, a, b) => clamp((t - a) / (b - a));
export const ease = (x) => x * x * (3 - 2 * x);
export const lerp = (a, b, k) => a + (b - a) * k;
export const fmt = (n) => Math.round(n).toLocaleString('en-US');

export function setFont(ctx, size, weight) {
  ctx.font = `${weight || 400} ${size}px ${FONT}`;
}

export function mw(ctx, s, size, weight) {
  setFont(ctx, size, weight);
  return ctx.measureText(String(s).toUpperCase()).width;
}

export function txt(ctx, s, x, y, o = {}) {
  setFont(ctx, o.size || 11, o.weight);
  ctx.fillStyle = o.color || C.ink;
  ctx.textAlign = o.align || 'left';
  ctx.textBaseline = 'alphabetic';
  ctx.fillText(String(s).toUpperCase(), x, y);
}

export function rr(ctx, x, y, w, h) {
  ctx.beginPath();
  ctx.rect(x, y, w, h);
}

// Blend two #rrggbb colours.
export function mix(a, b, k) {
  let o = '#';
  for (let i = 1; i < 7; i += 2) {
    const x = parseInt(a.substr(i, 2), 16), y = parseInt(b.substr(i, 2), 16);
    o += ('0' + Math.round(x + (y - x) * k).toString(16)).slice(-2);
  }
  return o;
}

// Deterministic PRNG (mulberry32) so every render of a scene is identical.
export function rng(a) {
  return function () {
    a |= 0; a = a + 0x6D2B79F5 | 0;
    let t = Math.imul(a ^ a >>> 15, 1 | a);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}
