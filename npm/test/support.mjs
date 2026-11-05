// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

export const npmRoot = fileURLToPath(new URL("../", import.meta.url));
const toolchain = spawnSync("go", ["env", "GOROOT"], { encoding: "utf8", timeout: 30_000 });
assert.equal(toolchain.status, 0, toolchain.stderr);
export const goExecutable = join(toolchain.stdout.trim(), "bin", process.platform === "win32" ? "go.exe" : "go");
export const workspace = mkdtempSync(join(tmpdir(), "gateway npm with spaces "));
const home = join(workspace, "home");
mkdirSync(home);
const userconfig = join(home, "npmrc");
const globalconfig = join(home, "global-npmrc");
writeFileSync(userconfig, "");
writeFileSync(globalconfig, "");
export const env = {
  PATH: process.env.PATH, SystemRoot: process.env.SystemRoot,
  HOME: home, USERPROFILE: home, TMPDIR: workspace, TEMP: workspace, TMP: workspace,
  GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", GOTELEMETRY: "off",
  ...(process.env.GOMAXPROCS ? { GOMAXPROCS: process.env.GOMAXPROCS } : {}),
  ...(process.env.GOFLAGS ? { GOFLAGS: process.env.GOFLAGS } : {}),
  GOCACHE: join(workspace, "go-cache"),
  npm_config_cache: join(workspace, "cache"), npm_config_userconfig: userconfig,
  npm_config_globalconfig: globalconfig, npm_config_audit: "false",
  npm_config_fund: "false", npm_config_update_notifier: "false",
  npm_config_fetch_retries: "0",
};

export const npmCLI = process.env.npm_execpath;
assert(npmCLI, "run through npm test");

export function run(executable, args, options = {}) {
  return spawnSync(executable, args, {
    cwd: npmRoot, env, encoding: "utf8", timeout: 30_000, ...options,
  });
}

export function runAsync(executable, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { cwd: npmRoot, env, ...options });
    let stdout = "", stderr = "";
    const timer = setTimeout(() => { child.kill(); reject(new Error("child timed out")); }, 30_000);
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("close", (status, signal) => { clearTimeout(timer); resolve({ status, signal, stdout, stderr }); });
    child.stdin.end(options.input ?? "");
  });
}

export function pack(directory, destination) {
  const result = run(process.execPath, [npmCLI, "pack", "--offline", "--ignore-scripts", "--json", "--pack-destination", destination], { cwd: directory });
  assert.equal(result.status, 0, result.stderr);
  return join(destination, JSON.parse(result.stdout)[0].filename);
}
