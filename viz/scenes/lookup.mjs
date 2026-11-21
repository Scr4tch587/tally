import { C, prog, ease, lerp, fmt, mw, txt, rr, rng, setFont } from '../lib/draw.mjs';

// The backlog as a field (x = time, y = amount). A search box closes in on the new event:
// first to the same amount, then to the same time window. Only what is left gets scored.
export default {
  name: 'candidate-lookup', label: 'Candidate lookup', dur: 11.5, fps: 20,
  setup: function (W, H) {
    var rnd = rng(23), px0 = 14, py0 = 34, pwd = W - 28, ph = H - 34 - 30;
    var ex = Math.round(px0 + 0.38 * pwd), ey = Math.round(py0 + 0.62 * ph);
    var n = Math.round(pwd * ph / 60), dots = [];
    while (dots.length < n * 2) {
      var x = px0 + rnd() * pwd, y = py0 + rnd() * ph;
      if (Math.abs(x - ex) < 24 && Math.abs(y - ey) < 11) continue;
      dots.push(x, y);
    }
    return { px0: px0, py0: py0, pwd: pwd, ph: ph, ex: ex, ey: ey, dots: dots };
  },
  draw: function (ctx, W, H, t, s) {
    var ex = s.ex, ey = s.ey, i;
    var k1 = ease(prog(t, 2, 3.6)), k2 = ease(prog(t, 4.2, 5.8));
    var yt = lerp(s.py0, ey - 7, k1), yb = lerp(s.py0 + s.ph, ey + 7, k1);
    var xl = lerp(s.px0, ex - 17, k2), xr = lerp(s.px0 + s.pwd, ex + 17, k2);
    var matched = t >= 7.6;
    // backlog
    var d = s.dots, pass;
    for (pass = 0; pass < 2; pass++) {
      ctx.fillStyle = C.pend; ctx.globalAlpha = pass ? 0.95 : 0.16;
      for (i = 0; i < d.length; i += 2) {
        var inside = d[i] >= xl && d[i] <= xr && d[i + 1] >= yt && d[i + 1] <= yb;
        if (inside === !!pass) ctx.fillRect(d[i] - 1.5, d[i + 1] - 1.5, 3, 3);
      }
    }
    ctx.globalAlpha = 1;
    // the two entries that survive both filters
    var pxp = ex - 9, pyp = ey + 2, dxp = ex + 10, dyp = ey - 3;
    ctx.fillStyle = matched ? C.match : C.pend; ctx.fillRect(pxp - 1.5, pyp - 1.5, 3, 3);
    ctx.fillStyle = C.pend; ctx.globalAlpha = matched ? 0.4 : 1; ctx.fillRect(dxp - 1.5, dyp - 1.5, 3, 3);
    ctx.globalAlpha = 1;
    // search box
    var ra = prog(t, 1.5, 1.9);
    if (ra > 0) {
      ctx.globalAlpha = ra; ctx.strokeStyle = C.hi; ctx.lineWidth = 1.5;
      ctx.strokeRect(xl, yt, xr - xl, yb - yt);
      ctx.globalAlpha = 1;
    }
    // the new event
    var ea = prog(t, 0.7, 1.0);
    if (ea > 0) {
      ctx.globalAlpha = ea; ctx.fillStyle = matched ? C.match : C.hi;
      ctx.fillRect(ex - 2.5, ey - 2.5, 5, 5);
      var ring = prog(t, 0.7, 1.6);
      if (ring < 1) { ctx.globalAlpha = 1 - ring; ctx.strokeStyle = C.hi; ctx.lineWidth = 1.5; ctx.strokeRect(ex - 4 - ring * 18, ey - 4 - ring * 18, 8 + ring * 36, 8 + ring * 36); }
      ctx.globalAlpha = 1;
    }
    // magnified view of what is left
    var za = ease(prog(t, 6.2, 6.7));
    if (za > 0) {
      var iw = 156, ih = 72, ix = ex + 40, iy = ey - 110;
      ctx.globalAlpha = za * 0.6; ctx.strokeStyle = C.hi; ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(xl, yt); ctx.lineTo(ix, iy + ih); ctx.moveTo(xr, yt); ctx.lineTo(ix + iw, iy + ih); ctx.stroke();
      ctx.globalAlpha = za;
      ctx.fillStyle = C.bg; ctx.fillRect(ix, iy, iw, ih);
      ctx.strokeStyle = C.hi; ctx.lineWidth = 1.5; ctx.strokeRect(ix, iy, iw, ih);
      var cxz = ix + 82, cyz = iy + 36, pxz = ix + 36, pyz = iy + 46, dxz = ix + 128, dyz = iy + 22;
      var lk = ease(prog(t, 7.0, 7.6));
      if (lk > 0) {
        ctx.strokeStyle = C.match; ctx.lineWidth = 2.5;
        ctx.beginPath(); ctx.moveTo(cxz, cyz); ctx.lineTo(lerp(cxz, pxz, lk), lerp(cyz, pyz, lk)); ctx.stroke();
      }
      ctx.fillStyle = matched ? C.match : C.hi; ctx.fillRect(cxz - 7, cyz - 7, 14, 14);
      ctx.fillStyle = matched ? C.match : C.pend; ctx.fillRect(pxz - 6, pyz - 6, 12, 12);
      ctx.globalAlpha = za * (matched ? 0.4 : 1);
      ctx.fillStyle = C.pend; ctx.fillRect(dxz - 6, dyz - 6, 12, 12);
      ctx.globalAlpha = 1;
    }
    // counter
    var v = 74570;
    if (k1 > 0) v = Math.exp(lerp(Math.log(74570), Math.log(310), k1));
    if (k2 > 0) v = Math.exp(lerp(Math.log(310), Math.log(2), k2));
    var num = fmt(v);
    txt(ctx, num, s.px0, 23, { size: 14, weight: 600, color: C.hi });
    txt(ctx, v < 2.5 ? 'to score' : 'to compare', s.px0 + mw(ctx, num, 14, 600) + 8, 23, { size: 10, color: C.mute });
    var fa = prog(t, 8.2, 8.7);
    if (fa > 0) { ctx.globalAlpha = fa; txt(ctx, '323x faster', s.px0 + s.pwd, 23, { size: 12, weight: 600, color: C.match, align: 'right' }); ctx.globalAlpha = 1; }
    // steps
    var steps = [['same amount', 2], ['same time window', 4.2], ['score the rest', 6.2]], third = s.pwd / 3;
    for (i = 0; i < 3; i++) {
      var on = t >= steps[i][1], sx = s.px0 + i * third;
      ctx.fillStyle = on ? C.hi : C.line; ctx.fillRect(sx, H - 18, 7, 7);
      txt(ctx, steps[i][0], sx + 12, H - 11, { size: 9.5, color: on ? C.hi : C.mute });
    }
  }
};
