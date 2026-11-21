import { C, prog, ease, lerp, fmt, mw, txt, rr, rng, setFont } from '../lib/draw.mjs';

var EREF = 'V418067061617140';
var COMPS = [['amount', 0.40, C.amount], ['time', 0.25, C.time], ['account', 0.15, C.account], ['text', 0.20, C.text]];
var CANDS = [
  { dt: 20, acct: 'A', ref: 'V41806706', sc: [1, 1 - 15 / 115, 1, 1] },
  { dt: 4, acct: 'A', ref: 'V52291338', sc: [1, 1, 1, 0] },
  { dt: 2, acct: 'B', ref: 'V41806706', sc: [1, 1, 0, 1] }
];
export default {
  name: 'scoring', label: 'Scoring', dur: 10.5, fps: 20,
  setup: function () { return {}; },
  draw: function (ctx, W, H, t) {
    var pad = 16, fs = W < 480 ? 10.5 : 12, i, k;
    var P0 = 1.6, PL = 1.45, VETO = 6.3, FINAL = 6.9;
    ctx.globalAlpha = prog(t, 0.1, 0.5);
    txt(ctx, 'BANK EVENT', pad, 24, { size: 9, color: C.mute });
    var pre = 'account A · ref ';
    txt(ctx, pre, pad, 44, { size: fs, color: C.mute });
    var erx = pad + mw(ctx, pre, fs);
    txt(ctx, EREF, erx, 44, { size: fs, color: C.hi });
    var lx = pad;
    for (i = 0; i < 4; i++) {
      ctx.fillStyle = COMPS[i][2]; ctx.fillRect(lx, 57, 8, 8);
      var lab = COMPS[i][0];
      txt(ctx, lab, lx + 12, 65, { size: 9.5, color: C.mute });
      lx += 12 + mw(ctx, lab, 9.5) + 12;
    }
    ctx.globalAlpha = 1;
    var top = 78, rh = (H - top - 10) / 3, bw = W - pad * 2 - 50;
    var anyMatch = false;
    for (i = 0; i < 3; i++) {
      var c = CANDS[i], y0 = top + i * rh;
      var ra = prog(t, 0.6 + i * 0.25, 0.9 + i * 0.25);
      if (ra <= 0) continue;
      var lp = (t - (P0 + i * PL)) / PL;
      var f = [prog(lp, 0, 0.2), prog(lp, 0.2, 0.38), prog(lp, 0.38, 0.52), 0];
      var sp = prog(lp, 0.52, 0.95), gk = Math.floor(sp * 4);
      f[3] = c.sc[3] > 0 ? Math.min(gk, 4) / 4 : 0;
      var vetoed = i === 2 && t >= VETO, win = i === 0 && t >= FINAL, lost = i === 1 && t >= FINAL;
      ctx.globalAlpha = ra * (lost ? 0.7 : 1);
      if (win) {
        rr(ctx, pad - 8, y0 - 1, W - pad * 2 + 16, 58, 5);
        ctx.strokeStyle = C.match; ctx.lineWidth = 1.5; ctx.stroke();
      }
      // meta line
      var a1 = 'account ';
      var mx = pad;
      txt(ctx, a1, mx, y0 + 14, { size: fs - 0.5, color: C.mute }); mx += mw(ctx, a1, fs - 0.5);
      var acctBad = i === 2 && lp >= 0.38;
      txt(ctx, c.acct, mx, y0 + 14, { size: fs - 0.5, color: acctBad ? C.fp : C.mute, weight: acctBad ? 600 : 400 });
      mx += mw(ctx, c.acct, fs - 0.5, acctBad ? 600 : 400);
      txt(ctx, ' · ref ', mx, y0 + 14, { size: fs - 0.5, color: C.mute }); mx += mw(ctx, ' · ref ', fs - 0.5);
      txt(ctx, c.ref, mx, y0 + 14, { size: fs - 0.5, color: C.hi });
      // n-gram window
      if (lp >= 0.52 && lp < 0.95 && gk < 4) {
        var gram = c.ref.slice(gk, gk + 6);
        var gx = mx + mw(ctx, c.ref.slice(0, gk), fs - 0.5), gw = mw(ctx, gram, fs - 0.5);
        var at = EREF.indexOf(gram), gc = at >= 0 ? C.match : C.fp;
        ctx.strokeStyle = gc; ctx.lineWidth = 1.5;
        ctx.strokeRect(gx - 1.5, y0 + 2.5, gw + 3, 15);
        if (at >= 0) {
          var ex = erx + mw(ctx, EREF.slice(0, at), fs), ew = mw(ctx, gram, fs);
          ctx.strokeRect(ex - 1.5, 31.5, ew + 3, 16);
          ctx.globalAlpha = ra * 0.45;
          ctx.beginPath(); ctx.moveTo(ex + ew / 2, 48); ctx.lineTo(gx + gw / 2, y0 + 2); ctx.stroke();
          ctx.globalAlpha = ra;
        }
      }
      if (lp >= 0.95 && c.sc[3] > 0) {
        anyMatch = true;
        ctx.fillStyle = C.match;
        ctx.fillRect(mx, y0 + 17, mw(ctx, c.ref, fs - 0.5), 1.5);
      }
      // stacked bar
      if (vetoed) ctx.globalAlpha = ra * 0.3;
      ctx.fillStyle = 'rgba(255,255,255,0.06)'; ctx.fillRect(pad, y0 + 22, bw, 12);
      var x = pad, sum = 0;
      for (k = 0; k < 4; k++) {
        var v = COMPS[k][1] * c.sc[k] * f[k];
        ctx.fillStyle = COMPS[k][2];
        ctx.fillRect(x, y0 + 22, v * bw, 12);
        x += v * bw; sum += v;
      }
      ctx.globalAlpha = ra * (lost ? 0.7 : 1);
      if (lp > 0) {
        txt(ctx, sum.toFixed(2), W - pad, y0 + 33, { size: 14, weight: 600, align: 'right', color: vetoed ? C.fp : win ? C.match : C.ink });
        if (vetoed) { ctx.fillStyle = C.fp; ctx.fillRect(W - pad - 36, y0 + 28, 37, 1.5); }
      }
      var status = null, scol = C.mute;
      if (vetoed) { status = 'veto: different account'; scol = C.fp; }
      else if (win) { status = 'confirmed'; scol = C.match; }
      else if (lost) { status = 'outranked'; }
      if (status) txt(ctx, status, pad, y0 + 50, { size: 9.5, color: scol, weight: 500 });
      ctx.globalAlpha = 1;
    }
    if (anyMatch) {
      setFont(ctx, fs);
      ctx.fillStyle = C.match;
      ctx.fillRect(erx, 47, ctx.measureText(EREF.slice(0, 9)).width, 1.5);
    }
    // threshold floor
    var ta = prog(t, 1.2, 1.6);
    if (ta > 0) {
      var tx = pad + 0.79 * bw;
      ctx.globalAlpha = ta;
      ctx.strokeStyle = C.mute; ctx.lineWidth = 1; ctx.setLineDash([3, 3]);
      ctx.beginPath(); ctx.moveTo(tx + 0.5, top + 19); ctx.lineTo(tx + 0.5, top + 2 * rh + 38); ctx.stroke();
      ctx.setLineDash([]);
      txt(ctx, 'floor', tx, top + 2 * rh + 50, { size: 9, color: C.mute, align: 'center' });
      ctx.globalAlpha = 1;
    }
  }
};
