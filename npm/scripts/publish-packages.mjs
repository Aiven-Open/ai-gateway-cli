// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { spawnSync } from "node:child_process";
import { join, resolve } from "node:path";
import { publishOrder, publishPackages } from "./publish.mjs";

const args = process.argv.slice(2);
const dryRun = args.includes("--dry-run");
const directories = args.filter((arg) => arg !== "--dry-run");
if (directories.length !== 1 || directories[0].startsWith("--")) {
  throw new Error("usage: node scripts/publish-packages.mjs <prepare-packages-output> [--dry-run]");
}
const npmCLI = process.env.npm_execpath;
if (!npmCLI) throw new Error("run through npm run publish-packages so npm_execpath is available");
const npm = (npmArgs) => spawnSync(process.execPath, [npmCLI, ...npmArgs], { encoding: "utf8", timeout: 300_000 });
const done = publishPackages(publishOrder(join(resolve(directories[0]), "artifacts")), { npm, dryRun });
process.stdout.write(`${JSON.stringify(done, null, 2)}\n`);
