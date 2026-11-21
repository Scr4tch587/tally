import { C, prog, ease, lerp, fmt, mw, txt, rr, rng, setFont } from '../lib/draw.mjs';

// Two ledger entries (notched) and two bank entries (toothed). Depths are in units.
// Each bank entry fills its own ledger entry exactly. In the other one it bottoms out early and sits loose.
var PROFILE = [[2, 0, 2, 1], [2, 1, 2, 0]];
function bankTile(ctx, xr, yc, g, teeth, col) {
  var seg = g.th / 4, k;
  ctx.fillStyle = col;
  ctx.fillRect(xr - g.tw, yc - g.th / 2, g.tw, g.th);
  for (k = 0; k < 4; k++) if (teeth[k]) ctx.fillRect(xr - 0.5, yc - g.th / 2 + k * seg, teeth[k] * g.u + 0.5, seg);
  if (g.label) txt(ctx, g.label, xr - g.tw / 2, yc + 4, { size: 11.5, weight: 600, color: '#000000', align: 'center' });
}
function ledgerTile(ctx, xl, yc, g, notch, col) {
  var seg = g.th / 4, k;
  ctx.fillStyle = col;
  ctx.fillRect(xl, yc - g.th / 2, g.tw, g.th);
  ctx.fillStyle = C.bg;
  for (k = 0; k < 4; k++) if (notch[k]) ctx.fillRect(xl - 0.5, yc - g.th / 2 + k * seg, notch[k] * g.u + 0.5, seg);
  if (g.label) txt(ctx, g.label, xl + 2 * g.u + (g.tw - 2 * g.u) / 2, yc + 4, { size: 11.5, weight: 600, color: '#000000', align: 'center' });
}
export default {
  name: 'deferred-confirmation', label: 'Deferred confirmation', dur: 15, fps: 20,
  setup: function () { return {}; },
  draw: function (ctx, W, H, t) {
    var pw = W / 2;
    ctx.strokeStyle = C.line; ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(pw + 0.5, 10); ctx.lineTo(pw + 0.5, H - 10); ctx.stroke();
    for (var p = 0; p < 2; p++) {
      ctx.save();
      ctx.beginPath(); ctx.rect(p * pw + 1, 0, pw - 1, H); ctx.clip();
      this.panel(ctx, p, pw, H, t);
      ctx.restore();
    }
  },
  panel: function (ctx, p, pw, H, t) {
    var x0 = p * pw, cx = x0 + pw / 2;
    var g = { tw: Math.min(pw * 0.26, 96), th: 48, u: 6 };
    g.label = g.tw >= 64 ? '$1,240' : null;
    var yc = 30 + (H - 30 - 12) / 2, yTop = yc - 36, yBot = yc + 36;
    var Lx = Math.round(cx + g.u / 2), dock = Lx - 1, start = x0 - 2 * g.u - 4;
    txt(ctx, p ? 'Wait, then decide' : 'Decide on arrival', cx, 22, { size: 12, weight: 600, align: 'center' });

    // arrivals, identical on both sides: ledger 1, bank 2, ledger 2, bank 1
    var kB2 = ease(prog(t, 1.5, 2.8)), kB1 = ease(prog(t, 4.8, 6.1));
    var b1x, b1y, b2x, b2y, col = C.pend;
    if (p === 0) {
      // each bank entry takes the only ledger entry available when it arrives: a loose fit
      b2x = lerp(start, dock - g.u, kB2); b2y = yTop;
      b1x = lerp(start, dock - g.u, kB1); b1y = yBot;
      if (t >= 6.8) col = C.fp;
    } else {
      var waitX = dock - 2 * g.u - 14, kx = ease(prog(t, 8.4, 9.6));
      b2x = lerp(lerp(start, waitX, kB2), dock, kx); b2y = lerp(yTop, yBot, kx);
      b1x = lerp(lerp(start, waitX, kB1), dock, kx); b1y = lerp(yBot, yTop, kx);
      if (t >= 9.7) col = C.match;
    }
    if (p === 1 && t >= 7.4 && t <= 8.45) {
      var sy = lerp(34, H - 16, (t - 7.4) / 1.0);
      var gr = ctx.createLinearGradient(0, sy - 30, 0, sy);
      gr.addColorStop(0, 'rgba(255,255,255,0)'); gr.addColorStop(1, 'rgba(255,255,255,0.16)');
      ctx.fillStyle = gr; ctx.fillRect(x0 + 6, sy - 30, pw - 12, 30);
      ctx.fillStyle = C.hi; ctx.fillRect(x0 + 6, sy, pw - 12, 1.5);
      txt(ctx, 'sweep', x0 + 10, sy - 5, { size: 9, color: C.hi });
    }
    ctx.globalAlpha = prog(t, 0.4, 0.9); ledgerTile(ctx, Lx, yTop, g, PROFILE[0], col);
    ctx.globalAlpha = prog(t, 3.4, 3.9); ledgerTile(ctx, Lx, yBot, g, PROFILE[1], col);
    ctx.globalAlpha = 1;
    if (t >= 1.5) bankTile(ctx, b2x, b2y, g, PROFILE[1], col);
    if (t >= 4.8) bankTile(ctx, b1x, b1y, g, PROFILE[0], col);
    // left: each pair is accepted the moment it docks
    if (p === 0 && t < 6.8) {
      ctx.strokeStyle = C.hi; ctx.lineWidth = 1.5;
      var ow = g.tw * 2 + g.u + 7, ox = dock - g.u - g.tw - 3;
      if (t >= 2.85) ctx.strokeRect(ox, yTop - g.th / 2 - 3, ow, g.th + 6);
      if (t >= 6.15) ctx.strokeRect(ox, yBot - g.th / 2 - 3, ow, g.th + 6);
    }
  }
};
