import { createHash } from "node:crypto";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sourceDir = path.join(root, "ui/static/src");
const outputDir = path.join(root, "ui/static/dist");

await mkdir(outputDir, { recursive: true });
for (const entry of await readdir(outputDir)) {
  if (entry !== ".gitkeep") {
    await rm(path.join(outputDir, entry), { recursive: true, force: true });
  }
}

const manifest = {};
for (const sourceName of ["app.css", "app.js"]) {
  const source = await readFile(path.join(sourceDir, sourceName));
  const hash = createHash("sha256").update(source).digest("hex").slice(0, 12);
  const extension = path.extname(sourceName);
  const base = path.basename(sourceName, extension);
  const outputName = `${base}-${hash}${extension}`;
  await writeFile(path.join(outputDir, outputName), source);
  manifest[sourceName] = `/assets/${outputName}`;
}

await writeFile(path.join(outputDir, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
