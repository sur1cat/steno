// Кадр анимированного SVG в заданную секунду. Внутрь страницы SVG кладётся
// разметкой, а не картинкой: только тогда доступны pauseAnimations и
// setCurrentTime, а <img> проигрывает своё время и снять точный момент нельзя.
import puppeteer from "puppeteer-core";
import fs from "node:fs";
const [, , svgPath, tRaw, out, cx, cy, cw, ch] = process.argv;
const t = Number(tRaw);
const svg = fs.readFileSync(svgPath, "utf8");
const b = await puppeteer.launch({
  executablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  headless: "new", args: ["--hide-scrollbars", "--force-color-profile=srgb"] });
const p = await b.newPage();
await p.setViewport({ width: 1200, height: 640, deviceScaleFactor: 2 });
await p.setContent(`<!doctype html><meta charset="utf-8">
<body style="margin:0">${svg}</body>`, { waitUntil: "load" });
await p.evaluate((time) => {
  const s = document.querySelector("svg");
  s.pauseAnimations();
  s.setCurrentTime(time);
}, t);
await new Promise(r => setTimeout(r, 120));
const clip = cw ? { x: +cx, y: +cy, width: +cw, height: +ch } : undefined;
await p.screenshot({ path: out, clip });
console.log("кадр", t + "s →", out);
await b.close();
