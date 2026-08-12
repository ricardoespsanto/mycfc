import { execFileSync } from "node:child_process";
import { readFile } from "node:fs/promises";

const files = execFileSync("git", ["ls-files", "*.templ"], { encoding: "utf8" })
  .trim()
  .split("\n")
  .filter(Boolean);

const standardAttributes = new Set([
  "boost", "confirm", "delete", "disable", "disabled-elt", "encoding", "ext",
  "get", "headers", "history", "history-elt", "include", "indicator", "inherit",
  "on", "params", "patch", "post", "preserve", "prompt", "push-url", "put",
  "replace-url", "request", "select", "select-oob", "swap", "swap-oob", "sync",
  "target", "trigger", "validate", "vals",
]);
const requestAttributes = new Set(["delete", "get", "patch", "post", "put"]);
const allowedSwapStrategies = new Set([
  "afterbegin", "afterend", "beforebegin", "beforeend", "delete", "innerHTML",
  "none", "outerHTML", "textContent",
]);
const errors = [];

for (const file of files) {
  const source = await readFile(file, "utf8");
  const ids = new Set([...source.matchAll(/\bid=["']([^"']+)["']/g)].map((match) => match[1]));
  const attributes = [...source.matchAll(/\bhx-([a-zA-Z0-9:_-]+)\s*=\s*(?:["']([^"']*)["']|\{[^}]*\})/g)];

  for (const match of attributes) {
    const name = match[1];
    const value = match[2];
    const baseName = name.replace(/-(?:[1-5][0-9]{2}|\*)$/, "").split(":", 1)[0];
    if (!standardAttributes.has(baseName)) {
      errors.push(`${file}: unknown HTMX attribute hx-${name}`);
      continue;
    }

    if (value !== undefined && baseName === "swap") {
      const strategy = value.trim().split(/\s+/, 1)[0];
      if (!allowedSwapStrategies.has(strategy)) {
        errors.push(`${file}: hx-${name} uses unknown swap strategy ${JSON.stringify(strategy)}`);
      }
    }

    if (value !== undefined && baseName === "target" && value.startsWith("#") && !ids.has(value.slice(1))) {
      errors.push(`${file}: hx-${name} references missing local id ${JSON.stringify(value)}`);
    }
  }

  for (const form of source.matchAll(/<form\b([^>]*)>/gs)) {
    const attributeSource = form[1];
    const request = [...attributeSource.matchAll(/\bhx-([a-z]+)\s*=/g)].find((candidate) => requestAttributes.has(candidate[1]));
    if (!request) continue;
    if (!/\baction\s*=/.test(attributeSource) || !/\bmethod\s*=/.test(attributeSource)) {
      errors.push(`${file}: form using hx-${request[1]} must retain action and method fallback attributes`);
    }
  }
}

if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exitCode = 1;
} else {
  console.log(`HTMX/templ checks passed for ${files.length} templates.`);
}
