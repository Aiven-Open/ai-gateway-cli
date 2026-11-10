// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { spawnSync } from "node:child_process";
import { chmodSync, cpSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { binaryName, packageName, targets } from "../lib/platforms.mjs";
import { inspectArtifacts, sha256 } from "./artifacts.mjs";
import { checkLinkedNotices } from "./notices.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const npmRoot = join(root, "npm");
const options = {};
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i];
  if (!["--out", "--version"].includes(key) || !process.argv[i + 1] || options[key]) {
    throw new Error("usage: prepare.mjs --out <new-directory> [--version <semver>]");
  }
  options[key] = process.argv[i + 1];
}
if (!options["--out"]) throw new Error("--out is required; use a new directory");
const output = resolve(options["--out"]);
const main = JSON.parse(readFileSync(join(npmRoot, "package.json"), "utf8"));
const version = options["--version"] ?? main.version;
if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error("version must be an explicit semver (no mutable latest reference)");
}
const command = "./cmd/aiven-ai-gateway";
const env = {
  ...process.env,
  npm_config_audit: "false", npm_config_fund: "false", npm_config_update_notifier: "false",
};
function run(executable, args, cwd = root, overrides = {}) {
  const result = spawnSync(executable, args, { cwd, env: { ...env, ...overrides }, encoding: "utf8", timeout: 600_000 });
  if (result.error || result.status !== 0) {
    throw new Error(`${executable} failed (${result.status}): ${result.stderr || result.error?.message}`);
  }
  return result.stdout.trim();
}
const commit = run("git", ["rev-parse", "HEAD"]);
const dirty = run("git", ["status", "--porcelain"]).length > 0;
const epoch = process.env.SOURCE_DATE_EPOCH ?? run("git", ["show", "-s", "--format=%ct", "HEAD"]);
if (!/^\d+$/.test(epoch)) throw new Error("SOURCE_DATE_EPOCH must be a Unix timestamp");
const date = new Date(Number(epoch) * 1000).toISOString();
const goVersion = run("go", ["version"]);
// The build inherits GOFLAGS, including values saved with go env -w, so provenance records them.
const goFlags = run("go", ["env", "GOFLAGS"]);
checkLinkedNotices(root, run);
mkdirSync(output); // Refuse to overwrite previous build evidence.
const artifacts = join(output, "artifacts");
mkdirSync(artifacts);
const packages = join(output, "packages");
mkdirSync(packages);
const mainDir = join(packages, "main");
mkdirSync(mainDir);
for (const file of ["bin", "lib", "README.md"]) cpSync(join(npmRoot, file), join(mainDir, file), { recursive: true });
for (const file of ["LICENSE", "THIRD_PARTY_NOTICES"]) cpSync(join(root, file), join(mainDir, file));
chmodSync(join(mainDir, "bin/aiven-ai-gateway.mjs"), 0o755);
main.version = version;
main.optionalDependencies = Object.fromEntries(targets.map((target) => [packageName(target), version]));
main.scripts = undefined;
writeFileSync(join(mainDir, "package.json"), `${JSON.stringify(main, null, 2)}\n`);
const npmCLI = process.env.npm_execpath;
if (!npmCLI) throw new Error("run through npm run prepare-packages so npm_execpath is available");
const pack = (directory) => run(process.execPath, [npmCLI, "pack", "--ignore-scripts", "--offline", "--json", "--pack-destination", artifacts], directory);
pack(mainDir);
for (const target of targets) {
  const directory = join(packages, `${target.os}-${target.cpu}`);
  mkdirSync(join(directory, "bin"), { recursive: true });
  const binary = join(directory, "bin", binaryName(target));
  const buildEnv = { CGO_ENABLED: "0", GOOS: target.goos, GOARCH: target.goarch, GOARM: target.goarm ?? "", SOURCE_DATE_EPOCH: epoch };
  const cgoPackages = run("go", ["list", "-deps", "-f", "{{if .CgoFiles}}{{.ImportPath}}{{end}}", command], root, buildEnv);
  if (cgoPackages) throw new Error(`CGO dependencies found: ${cgoPackages}`);
  const ldflags = `-s -w -buildid= -X main.version=${version} -X main.commit=${commit} -X main.date=${date}`;
  run("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", binary, command], root, buildEnv);
  chmodSync(binary, 0o755);
  const metadata = {
    name: packageName(target), version, description: `AI Gateway CLI for ${target.os}/${target.cpu}`,
    license: main.license, repository: main.repository, os: [target.os], cpu: [target.cpu],
    files: ["bin/", "provenance.json", "LICENSE", "THIRD_PARTY_NOTICES"],
  };
  for (const file of ["LICENSE", "THIRD_PARTY_NOTICES"]) cpSync(join(root, file), join(directory, file));
  writeFileSync(join(directory, "package.json"), `${JSON.stringify(metadata, null, 2)}\n`);
  writeFileSync(join(directory, "provenance.json"), `${JSON.stringify({
    version, commit, dirty, sourceDateEpoch: epoch, goVersion, goFlags, command, target,
    cgoEnabled: "0", ldflags, sha256: sha256(readFileSync(binary)),
  }, null, 2)}\n`);
  pack(directory);
}
const summary = inspectArtifacts(artifacts);
writeFileSync(join(output, "artifacts.json"), `${JSON.stringify(summary, null, 2)}\n`);
process.stdout.write(`${JSON.stringify({ version, commit, dirty, command, artifacts, summary }, null, 2)}\n`);
