import { createHash } from "node:crypto";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sourceDir = path.join(root, "ui/static/src");
const outputDir = path.join(root, "ui/static/dist");
const compatibilityOutputs = {
  "app.css": ["app-bd81fcee5117.css"],
  "app.js": ["app-d0ef35f67950.js"],
};

await mkdir(outputDir, { recursive: true });
for (const entry of await readdir(outputDir)) {
  if (entry !== ".gitkeep") {
    await rm(path.join(outputDir, entry), { recursive: true, force: true });
  }
}

const manifest = {};
for (const sourceName of ["app.css", "app.js", "images/cfc-logo.png", "images/cfc-hero.png"]) {
	const source = await readFile(path.join(sourceDir, sourceName));
	const hash = createHash("sha256").update(source).digest("hex").slice(0, 12);
	const extension = path.extname(sourceName);
	const base = path.basename(sourceName, extension);
  const outputName = `${base}-${hash}${extension}`;
  await writeFile(path.join(outputDir, outputName), source);
  // The Hetzner release agent validates the asset recorded in its installed
  // deployment bundle. Keep that path available until the host bundle is
  // upgraded independently of the application image.
  for (const compatibilityOutput of compatibilityOutputs[sourceName] ?? []) {
    await writeFile(path.join(outputDir, compatibilityOutput), source);
  }
  manifest[sourceName] = `/assets/${outputName}`;
}

await writeFile(path.join(outputDir, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
