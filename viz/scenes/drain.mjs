// The hero: every event in the BenchRec replay is one cell, in the order it was
// ingested. Cells light up as events arrive and clear as the sweep matches them.
// What is left at the end is recoloured by the reason it was not matched.
//
// Driven entirely by viz/data/drain-trace.csv, written by
//   go run ./cmd/benchrec -limit 0 -reset -max-wait-sec 480 -trace viz/data/drain-trace.csv
import { createCanvas } from '@napi-rs/canvas';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { C, prog, ease, fmt, mw, txt, mix } from '../lib/draw.mjs';

const TRACE = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'data', 'drain-trace.csv');
const COLS = 520, CELL = 2, GRID_Y = 8;
const T0 = 0.5, T1 = 12;          // clip seconds over which the real run plays
const FLASH = 0.14;               // clip seconds a cell flashes when it is matched

// #rrggbb -> little-endian RGBA word for ImageData
function word(hex) {
  const r = parseInt(hex.substr(1, 2), 16), g = parseInt(hex.substr(3, 2), 16), b = parseInt(hex.substr(5, 2), 16);
  return (0xff000000 | (b << 16) | (g << 8) | r) >>> 0;
}

// number of values in a sorted array that are <= x
function countUpTo(sorted, x) {
  let lo = 0, hi = sorted.length;
  while (lo < hi) { const mid = (lo + hi) >> 1; if (sorted[mid] <= x) lo = mid + 1; else hi = mid; }
  return lo;
}

export default {
  name: 'drain', label: 'BenchRec', pixel: true, W: COLS * CELL, H: 700, bar: 26, dur: 19, fps: 15,

  setup() {
    const lines = readFileSync(TRACE, 'utf8').trim().split('\n').slice(1);
    const n = lines.length;
    const ingest = new Int32Array(n), matched = new Int32Array(n), outcome = new Uint8Array(n);
    const counts = { T: 0, R: 0, W: 0, M: 0, G: 0, O: 0 };
    const matchTimes = [], wrongTimes = [];
    let endMs = 0;
    for (let i = 0; i < n; i++) {
      const [a, b, o] = lines[i].split(',');
      ingest[i] = +a; matched[i] = +b; outcome[i] = o.charCodeAt(0);
      counts[o]++;
      if (matched[i] >= 0) { matchTimes.push(matched[i]); if (o === 'W') wrongTimes.push(matched[i]); }
      endMs = Math.max(endMs, ingest[i], matched[i]);
    }
    matchTimes.sort((x, y) => x - y);
    wrongTimes.sort((x, y) => x - y);
    const rows = Math.ceil(n / COLS);
    this.label = `BenchRec ${fmt(n)} evt`;
    const small = createCanvas(COLS, rows);
    const sctx = small.getContext('2d');
    const image = sctx.createImageData(COLS, rows);
    const wrongMatches = counts.W / 2, trueMatches = counts.T / 2;
    return {
      n, rows, ingest, matched, outcome, counts, matchTimes, wrongTimes, endMs,
      small, sctx, image, px: new Uint32Array(image.data.buffer),
      summary: [
        ['No counterpart', counts.O, `${(100 * counts.O / n).toFixed(1)}% of events`, 'exists in one system only', C.pend],
        ['Many-to-one group', counts.G, `${(100 * counts.G / n).toFixed(1)}% of events`, 'needs N:M matching, not built', C.nm],
        ['Missed by Tally', counts.M, `${(100 * counts.M / n).toFixed(1)}% of events`, 'a real pair Tally did not find', C.miss],
        ['Wrong match', wrongMatches, `${(100 * wrongMatches / (wrongMatches + trueMatches)).toFixed(1)}% of scored matches`, 'paired with the wrong entry', C.fp],
      ],
    };
  },

  draw(ctx, W, H, t, s) {
    const real = prog(t, T0, T1) * s.endMs;                    // where the real run is, in ms
    const flashMs = FLASH / (T1 - T0) * s.endMs;
    const hl = ease(prog(t, T1 + 0.3, T1 + 1.1));              // end: dim the matched, show the leftovers
    const started = t >= T0;

    const cEmpty = word(C.cell), cPend = word(C.pend), cFlash = word(C.flash), cWrong = word(C.fp);
    const cMatch = word(mix(C.matchDim, '#03120e', hl));
    const cGroup = word(mix(C.pend, C.nm, hl)), cMissed = word(mix(C.pend, C.miss, hl));
    const G = 71, M = 77, Wc = 87;                             // 'G', 'M', 'W'
    const { px, ingest, matched, outcome, n } = s;
    px.fill(word(C.bg));
    for (let i = 0; i < n; i++) {
      let c = cEmpty;
      if (started && real >= ingest[i]) {
        const m = matched[i];
        if (m < 0 || real < m) c = outcome[i] === G ? cGroup : outcome[i] === M ? cMissed : cPend;
        else if (outcome[i] === Wc) c = cWrong;
        else c = (real - m < flashMs && hl === 0) ? cFlash : cMatch;
      }
      px[i] = c;
    }
    s.sctx.putImageData(s.image, 0, 0);
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(s.small, 0, GRID_Y, COLS * CELL, s.rows * CELL);

    // clock, in the header strip
    txt(ctx, `t+${Math.round(real / 1000)}s`, W - 12, -9, { size: 13, color: C.mute, align: 'right' });

    // running counters
    const gy = GRID_Y + s.rows * CELL, ox = 12;
    const arrived = started ? countUpTo(ingest, real) : 0;
    const done = started ? countUpTo(s.matchTimes, real) : 0;
    const wrong = started ? countUpTo(s.wrongTimes, real) / 2 : 0;
    if (hl < 1) {
      ctx.globalAlpha = 1 - hl;
      const items = [['Pending', arrived - done, C.pend], ['Matched', done, C.match], ['Wrong matches', wrong, C.fp]];
      items.forEach(([label, value, color], i) => {
        const x = ox + i * (W - 2 * ox) / 3;
        txt(ctx, label, x, gy + 32, { size: 13, color: C.mute });
        txt(ctx, fmt(value), x, gy + 66, { size: 28, weight: 600, color });
      });
      ctx.globalAlpha = 1;
    }

    // end summary: what is left, and why
    const sa = prog(t, T1 + 0.9, T1 + 1.4);
    if (sa > 0) {
      ctx.globalAlpha = sa;
      const cw = mw(ctx, '0', 15);
      s.summary.forEach(([label, count, rate, why, color], i) => {
        const y = gy + 30 + i * 22;
        ctx.fillStyle = color; ctx.fillRect(ox, y - 11, 11, 11);
        txt(ctx, label, ox + 22, y, { size: 15, color });
        txt(ctx, fmt(count), ox + 22 + cw * 26, y, { size: 15, color: C.hi, align: 'right' });
        txt(ctx, rate, ox + 22 + cw * 28, y, { size: 15 });
        txt(ctx, why, ox + 22 + cw * 52, y, { size: 15, color: C.mute });
      });
      ctx.globalAlpha = 1;
    }
  },
};
