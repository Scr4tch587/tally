// Renders every scene to docs/media/<name>.gif (for the README) and
// viz/out/<name>.mp4 (for anywhere that can play video).
//
//   node render.mjs                 render everything
//   node render.mjs scoring drain   render the named scenes
//   node render.mjs --still 8 drain write one PNG frame at t=8s to viz/out/
//
// A scene is a pure function of time: { name, label, dur, fps, setup(W, H), draw(ctx, W, H, t, state) }.
// Card scenes are drawn in a 640-wide logical space and scaled up; a scene can
// set `pixel` to take a canvas at exact device size instead.
import { createCanvas, GlobalFonts } from '@napi-rs/canvas';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { C, txt, mw } from './lib/draw.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const OUT = path.join(here, 'out');
const MEDIA = path.join(here, '..', 'docs', 'media');
const SCENES = ['drain', 'deferred', 'lookup', 'scoring'];

for (const weight of ['Regular', 'Medium', 'SemiBold']) {
  GlobalFonts.registerFromPath(
    path.join(here, 'node_modules', 'jetbrains-mono', 'fonts', 'webfonts', `JetBrainsMono-${weight}.woff2`),
    'Tally Mono');
}

// Card scenes: 640x360 logical plus a 20px header strip, scaled to 800 wide.
const CARD = { W: 640, H: 360, bar: 20, scale: 1.25 };

function geometry(scene) {
  if (scene.pixel) return { W: scene.W, H: scene.H, bar: scene.bar, scale: 1, outW: scene.W, outH: scene.H + scene.bar };
  return { ...CARD, outW: CARD.W * CARD.scale, outH: (CARD.H + CARD.bar) * CARD.scale };
}

function makeScanlines(scale) {
  const c = createCanvas(2, 3);
  const g = c.getContext('2d');
  g.fillStyle = 'rgba(0,0,0,0.10)';
  g.fillRect(0, 2, 2, 1);
  return c;
}

function drawFrame(ctx, scene, geo, state, t, scan) {
  const { W, H, bar, scale } = geo;
  ctx.setTransform(scale, 0, 0, scale, 0, 0);
  ctx.globalAlpha = 1;
  ctx.fillStyle = C.bg;
  ctx.fillRect(0, 0, W, H + bar);

  // header: the scene's label in black on an amber block
  const hs = bar * 0.5;
  const hw = mw(ctx, scene.label, hs, 600) + bar * 0.8;
  ctx.fillStyle = C.pend;
  ctx.fillRect(0, 0, hw, bar - 2);
  txt(ctx, scene.label, bar * 0.4, bar * 0.65, { size: hs, weight: 600, color: '#000000' });
  ctx.fillStyle = C.line;
  ctx.fillRect(0, bar - 2, W, 1);

  ctx.save();
  ctx.translate(0, bar);
  scene.draw(ctx, W, H, t, state);
  ctx.restore();

  if (scan) {
    ctx.fillStyle = ctx.createPattern(scan, 'repeat');
    ctx.fillRect(0, 0, W, H + bar);
  }
  // fade through black at the loop point
  const f = Math.max(1 - t / 0.35, (t - (scene.dur - 0.35)) / 0.35, 0);
  if (f > 0) {
    ctx.globalAlpha = Math.min(f, 1);
    ctx.fillStyle = C.bg;
    ctx.fillRect(0, 0, W, H + bar);
    ctx.globalAlpha = 1;
  }
}

function run(cmd, args) {
  return new Promise((resolve, reject) => {
    const p = spawn(cmd, args, { stdio: ['ignore', 'ignore', 'pipe'] });
    let err = '';
    p.stderr.on('data', (d) => { err += d; });
    p.on('close', (code) => (code === 0 ? resolve() : reject(new Error(`${cmd} exited ${code}\n${err.slice(-2000)}`))));
  });
}

async function renderScene(key, still) {
  const scene = (await import(`./scenes/${key}.mjs`)).default;
  const geo = geometry(scene);
  const canvas = createCanvas(geo.outW, geo.outH);
  const ctx = canvas.getContext('2d');
  const state = await scene.setup(geo.W, geo.H);
  const scan = scene.pixel ? null : makeScanlines();

  if (still !== null) {
    drawFrame(ctx, scene, geo, state, still, scan);
    const file = path.join(OUT, `${scene.name}-${still}s.png`);
    writeFileSync(file, canvas.toBuffer('image/png'));
    console.log(`still ${file}`);
    return;
  }

  const fps = scene.fps || 20;
  const frames = Math.round(scene.dur * fps);
  const master = path.join(OUT, `${scene.name}.mkv`);
  const palette = path.join(OUT, `${scene.name}-palette.png`);
  const gif = path.join(MEDIA, `${scene.name}.gif`);
  const mp4 = path.join(OUT, `${scene.name}.mp4`);

  // 1. frames -> lossless master
  const enc = spawn('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'rawvideo', '-pix_fmt', 'rgba',
    '-s', `${geo.outW}x${geo.outH}`, '-r', String(fps), '-i', '-', '-c:v', 'ffv1', master],
    { stdio: ['pipe', 'ignore', 'inherit'] });
  const closed = once(enc, 'close');
  for (let i = 0; i < frames; i++) {
    drawFrame(ctx, scene, geo, state, i / fps, scan);
    const data = ctx.getImageData(0, 0, geo.outW, geo.outH).data;
    if (!enc.stdin.write(Buffer.from(data.buffer, data.byteOffset, data.byteLength))) await once(enc.stdin, 'drain');
  }
  enc.stdin.end();
  const [code] = await closed;
  if (code !== 0) throw new Error(`ffmpeg master encode exited ${code}`);

  // 2. master -> gif (fixed palette, no dithering: the scenes are flat colour)
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', master, '-vf', 'palettegen=max_colors=96:stats_mode=full', palette]);
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', master, '-i', palette, '-lavfi',
    'paletteuse=dither=none:diff_mode=rectangle', '-loop', '0', gif]);
  // 3. master -> mp4
  await run('ffmpeg', ['-y', '-loglevel', 'error', '-i', master, '-c:v', 'libx264', '-pix_fmt', 'yuv420p',
    '-vf', 'pad=ceil(iw/2)*2:ceil(ih/2)*2', '-crf', '16', '-movflags', '+faststart', mp4]);
  rmSync(master); rmSync(palette);
  console.log(`${scene.name}: ${frames} frames at ${fps}fps, ${geo.outW}x${geo.outH} -> ${path.relative(process.cwd(), gif)}`);
}

const args = process.argv.slice(2);
let still = null;
const si = args.indexOf('--still');
if (si >= 0) { still = Number(args[si + 1]); args.splice(si, 2); }
mkdirSync(OUT, { recursive: true });
mkdirSync(MEDIA, { recursive: true });
for (const key of (args.length ? args : SCENES)) await renderScene(key, still);
