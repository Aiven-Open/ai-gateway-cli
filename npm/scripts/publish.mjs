// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { inspectArtifacts } from "./artifacts.mjs";

export const integrity = (bytes) => `sha512-${createHash("sha512").update(bytes).digest("base64")}`;
// A prerelease must never move `latest`.
export const distTag = (version) => (version.includes("-") ? "next" : "latest");

// Returns the published integrity, or undefined when the package or the version does not exist: npm reports both
// as exit 1 with a JSON E404. Anything else, including a published version without an integrity, is a failure,
// because the bytes cannot then be compared.
function publishedIntegrity(npm, { name, version }) {
  const view = npm(["view", `${name}@${version}`, "dist.integrity", "--json"]);
  let output;
  try {
    output = JSON.parse(view.stdout);
  } catch {
    output = undefined;
  }
  if (view.status === 0 && typeof output === "string" && output.startsWith("sha512-")) return output;
  if (view.status !== 0 && output?.error?.code === "E404") return undefined;
  throw new Error(`registry lookup failed: ${view.stderr.trim() || view.stdout.trim() || view.error?.message}`);
}

// Publishes in the given order and stops at the first failure. A published version is never replaced: the same
// bytes are skipped, so a rerun completes a partial release, and different bytes fail.
export function publishPackages(packages, { npm, dryRun = false }) {
  const done = [];
  for (const [index, pkg] of packages.entries()) {
    try {
      const published = publishedIntegrity(npm, pkg);
      if (published === undefined) {
        if (!dryRun) {
          const result = npm(["publish", pkg.file, "--access", "public", "--provenance", "--tag", distTag(pkg.version)]);
          if (result.status !== 0) throw new Error(`publish failed: ${result.stderr.trim() || result.error?.message}`);
        }
        done.push({ name: pkg.name, action: dryRun ? "would publish" : "published" });
      } else if (published === integrity(readFileSync(pkg.file))) {
        done.push({ name: pkg.name, action: "already published" });
      } else {
        throw new Error("a different build of this version is already published; release a new version");
      }
    } catch (error) {
      const rest = packages.slice(index + 1).map(({ name }) => name);
      throw new Error(`${pkg.name}@${pkg.version}: ${error.message}\ncompleted: ${JSON.stringify(done)}\nnot attempted: ${JSON.stringify(rest)}`);
    }
  }
  return done;
}

// The wrapper pins exact native versions, so it goes last: it must never be installable before they are.
export function publishOrder(artifacts) {
  const natives = inspectArtifacts(artifacts).map(({ name, version, archive }) => ({ name, version, file: join(artifacts, archive) }));
  const nativeFiles = new Set(natives.map(({ file }) => file));
  const [main] = readdirSync(artifacts).filter((file) => file.endsWith(".tgz"))
    .map((file) => join(artifacts, file)).filter((file) => !nativeFiles.has(file));
  return [...natives, { name: "@aivenio/ai-gateway", version: natives[0].version, file: main }];
}
