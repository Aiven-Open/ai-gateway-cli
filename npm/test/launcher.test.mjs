// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { after, test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { once } from "node:events";
import { pathToFileURL } from "node:url";
import { targets, binaryName, packageName, selectTarget } from "../lib/platforms.mjs";
import { env, goExecutable, npmCLI, npmRoot, pack, run, runAsync, workspace } from "./support.mjs";

after(() => rmSync(workspace, { recursive: true, force: true, maxRetries: 3 }));
const metadata = JSON.parse(readFileSync(join(npmRoot, "package.json")));
const target = selectTarget(process.platform, process.arch, process.config.variables.arm_version);
const name = packageName(target);
const fixture = join(workspace, binaryName(target));
const build = run(goExecutable, ["build", "-trimpath", "-o", fixture, "./test/fixture"], { timeout: 120_000 });
assert.equal(build.status, 0, build.stderr);
let sequence = 0;
function installation() {
  const root = join(workspace, `installed ${sequence++}`);
  mkdirSync(root);
  for (const file of ["bin", "lib", "package.json", "README.md"]) {
    cpSync(join(npmRoot, file), join(root, file), { recursive: true });
  }
  const native = join(root, "node_modules", name);
  mkdirSync(join(native, "bin"), { recursive: true });
  copyFileSync(fixture, join(native, "bin", binaryName(target)));
  chmodSync(join(native, "bin", binaryName(target)), 0o755);
  writeFileSync(join(native, "package.json"), JSON.stringify({
    name, version: metadata.version, os: [target.os], cpu: [target.cpu], files: ["bin/"],
  }));
  return { root, native, wrapper: join(root, "bin/aiven-ai-gateway.mjs") };
}

test("seven exact platform selections; unsupported CPU/OS and pre-ARMv7 fail", () => {
  assert.equal(targets.length, 7);
  assert.deepEqual(targets.map(packageName), [
    "@aiven/ai-gateway-linux-x64", "@aiven/ai-gateway-linux-arm64",
    "@aiven/ai-gateway-linux-arm", "@aiven/ai-gateway-darwin-x64",
    "@aiven/ai-gateway-darwin-arm64", "@aiven/ai-gateway-win32-x64",
    "@aiven/ai-gateway-win32-arm64",
  ]);
  for (const item of targets) assert.deepEqual(selectTarget(item.os, item.cpu, 7), item);
  for (const [os, cpu, arm] of [["freebsd", "x64"], ["linux", "ia32"], ["linux", "arm", 6]]) {
    assert.throws(() => selectTarget(os, cpu, arm), /unsupported platform/);
  }
});

test("full literal argv, empty/bare invocation, streams, environment and statuses", () => {
  const { wrapper } = installation();
  for (const status of [0, 7, 23, 127]) {
    const argv = ["launch", "codex", "--", "two words", "", `quote"slash\\`, "$HOME; & | *", "日本語"];
    const result = run(process.execPath, [wrapper, ...argv], {
      input: "literal stdin",
      env: { ...env, AIVEN_NPM_TEST_ENV: "synthetic-env", AIVEN_NPM_TEST_STATUS: String(status) },
    });
    assert.equal(result.status, status, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), { Argv: argv, Input: "literal stdin", Env: "synthetic-env" });
    assert.equal(result.stderr, "fixture stderr");
  }
  assert.deepEqual(JSON.parse(run(process.execPath, [wrapper]).stdout).Argv, []);
});

test("resolution ignores PATH", () => {
  const { wrapper } = installation();
  const result = run(process.execPath, [wrapper, "--help"], { env: { ...env, PATH: "" } });
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout).Argv, ["--help"]);
});

for (const fault of ["missing-package", "version", "identity", "platform", "missing-binary", "invalid-metadata", "not-executable", "spawn-failure"]) {
  test(`fails clearly without secret argv: ${fault}`, () => {
    const { wrapper, native } = installation();
    const binary = join(native, "bin", binaryName(target));
    const manifestFile = join(native, "package.json");
    const nativeMetadata = JSON.parse(readFileSync(manifestFile));
    if (fault === "missing-package") rmSync(native, { recursive: true });
    if (fault === "version") nativeMetadata.version = "99.0.0";
    if (fault === "identity") nativeMetadata.name = "@aiven/wrong";
    if (fault === "platform") nativeMetadata.cpu = ["wrong"];
    if (["version", "identity", "platform"].includes(fault)) writeFileSync(manifestFile, JSON.stringify(nativeMetadata));
    if (fault === "missing-binary") rmSync(binary);
    if (fault === "invalid-metadata") writeFileSync(manifestFile, "{bad-json");
    if (fault === "not-executable" && process.platform !== "win32") chmodSync(binary, 0o644);
    if (fault === "not-executable" && process.platform === "win32") {
      // A directory is never a runnable native file on either platform.
      rmSync(binary); mkdirSync(binary);
    }
    if (fault === "spawn-failure") writeFileSync(binary, "#!/no-such-interpreter\n");
    const result = run(process.execPath, [wrapper, "synthetic-secret-argv"]);
    assert.equal(result.status, 1, result.stderr);
    assert.equal(result.stdout, "");
    assert.match(result.stderr, /aiven-ai-gateway: /);
    assert.doesNotMatch(result.stderr, /synthetic-secret-argv/);
  });
}

test("a native symlink back to the wrapper is refused", { skip: process.platform === "win32" ? "Windows symlink privilege is not required for installation" : false }, () => {
  const { wrapper, native } = installation();
  const binary = join(native, "bin", binaryName(target));
  rmSync(binary);
  symlinkSync(wrapper, binary);
  const result = run(process.execPath, [wrapper]);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /points to the npm wrapper/);
});

// SIGINT goes to the process group, as a terminal's Ctrl-C does; the wrapper forwards the others.
for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  test(`delivers and preserves ${signal}`, { skip: process.platform === "win32" ? "POSIX signal identity does not exist on Windows" : false }, async () => {
    const { wrapper } = installation();
    const child = spawn(process.execPath, [wrapper], { env: { ...env, AIVEN_NPM_TEST_WAIT: "1" }, detached: signal === "SIGINT" });
    const closed = once(child, "close");
    const timer = setTimeout(() => child.kill("SIGKILL"), 10_000);
    try {
      const [data] = await once(child.stdout, "data");
      assert.match(data.toString(), /ready/);
      process.kill(signal === "SIGINT" ? -child.pid : child.pid, signal);
      const [code, observed] = await closed;
      assert.equal(code, null);
      assert.equal(observed, signal);
    } finally {
      clearTimeout(timer);
      child.stdin.destroy();
    }
  });
}

test("Ctrl-C is left to the tool", async () => {
  const { root } = installation();
  // The emitted event runs the handler a Ctrl-C runs; the terminal or console also delivers Ctrl-C to the tool itself.
  // Without a handler a real Ctrl-C ends Node while an emitted one does nothing, so exit 9 marks a missing handler.
  const launcher = pathToFileURL(join(root, "lib/launcher.mjs")).href;
  const script = `import { run } from ${JSON.stringify(launcher)}; run([]); if (!process.emit("SIGINT")) process.exit(9);`;
  const result = await runAsync(process.execPath, ["--input-type=module", "-e", script], {
    env: { ...env, AIVEN_NPM_TEST_WAIT: "1" },
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /ready/);
});

test("a tool ended by SIGPIPE exits 141", { skip: process.platform === "win32" ? "POSIX signal identity does not exist on Windows" : false }, async () => {
  const { wrapper } = installation();
  const child = spawn(process.execPath, [wrapper], { env: { ...env, AIVEN_NPM_TEST_FLOOD: "1" } });
  const closed = once(child, "close");
  const timer = setTimeout(() => child.kill("SIGKILL"), 10_000);
  try {
    await once(child.stdout, "data");
    child.stdout.destroy();
    const [code, signal] = await closed;
    assert.deepEqual([code, signal], [141, null]);
  } finally {
    clearTimeout(timer);
  }
});

test("registry fixture installs only matching optional binary; global/local/npx and offline cache", async (t) => {
  const { root, native } = installation();
  const tarDir = join(workspace, "registry tarballs");
  mkdirSync(tarDir);
  // Pack each metadata variant: npm must never fetch the other six tarballs.
  const packages = new Map([["@aiven/ai-gateway", { metadata, tarball: pack(root, tarDir) }]]);
  for (const item of targets) {
    const directory = join(workspace, `registry-${item.os}-${item.cpu}`);
    cpSync(native, directory, { recursive: true });
    const nativeMetadata = { name: packageName(item), version: metadata.version, os: [item.os], cpu: [item.cpu], files: ["bin/"] };
    writeFileSync(join(directory, "package.json"), JSON.stringify(nativeMetadata));
    packages.set(nativeMetadata.name, { metadata: nativeMetadata, tarball: pack(directory, tarDir) });
  }
  const downloaded = [];
  const server = createServer((request, response) => {
    const requested = decodeURIComponent(request.url.slice(1));
    if (requested.startsWith("tarballs/")) {
      const pkg = [...packages.values()].find((pkg) => requested === `tarballs/${pkg.metadata.name.replace("@aiven/", "")}.tgz`);
      if (!pkg) { response.writeHead(404).end(); return; }
      downloaded.push(pkg.metadata.name);
      response.end(readFileSync(pkg.tarball));
      return;
    }
    const pkg = packages.get(requested);
    if (!pkg) { response.writeHead(404).end(); return; }
    const integrity = createHash("sha512").update(readFileSync(pkg.tarball)).digest("base64");
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({
      name: requested, "dist-tags": { latest: metadata.version },
      versions: { [metadata.version]: {
        ...pkg.metadata, dist: { tarball: `${registry}tarballs/${requested.replace("@aiven/", "")}.tgz`, integrity: `sha512-${integrity}` },
      } },
    }));
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const registry = `http://127.0.0.1:${server.address().port}/`;
  t.after(() => server.close());
  const prefix = join(workspace, "global prefix");
  const npmEnv = {
    ...env, npm_config_registry: registry,
    npm_config_cache: join(workspace, "registry-cache"),
  };
  const install = await runAsync(process.execPath, [npmCLI, "install", "-g", "--prefix", prefix, `@aiven/ai-gateway@${metadata.version}`], { env: npmEnv });
  assert.equal(install.status, 0, install.stderr);
  assert.deepEqual(downloaded.sort(), ["@aiven/ai-gateway", name].sort());
  const globalRoot = process.platform === "win32" ? prefix : join(prefix, "lib");
  const globalEntry = join(globalRoot, "node_modules/@aiven/ai-gateway/bin/aiven-ai-gateway.mjs");
  assert.equal(run(process.execPath, [globalEntry], { env }).status, 0);
  const local = join(workspace, "local project");
  mkdirSync(local);
  writeFileSync(join(local, "package.json"), '{"private":true}');
  const localInstall = await runAsync(process.execPath, [npmCLI, "install", "--offline", `@aiven/ai-gateway@${metadata.version}`], { cwd: local, env: npmEnv });
  assert.equal(localInstall.status, 0, localInstall.stderr);
  const argv = ["launch", "codex", "--", "space here", "", "$HOME;&"];
  // npm exec caches full metadata separately from npm install's abbreviated
  // metadata. Warm the actual npx path before requiring offline reuse.
  const warmed = await runAsync(process.execPath, [
    npmCLI, "exec", "--yes", `--package=@aiven/ai-gateway@${metadata.version}`, "--", "aiven-ai-gateway",
  ], { cwd: workspace, env: npmEnv });
  assert.equal(warmed.status, 0, warmed.stderr);
  for (const args of [
    ["exec", "--offline", "--", "aiven-ai-gateway", ...argv],
    ["exec", "--offline", "--yes", `--package=@aiven/ai-gateway@${metadata.version}`, "--", "aiven-ai-gateway", ...argv],
  ]) {
    const result = await runAsync(process.execPath, [npmCLI, ...args], {
      cwd: args.includes("--yes") ? workspace : local,
      env: { ...npmEnv, AIVEN_NPM_TEST_STATUS: "23" },
    });
    assert.equal(result.status, 23, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout).Argv, argv);
  }
  assert.deepEqual(downloaded.sort(), ["@aiven/ai-gateway", name].sort(), "offline runs must not request any tarball");
  t.diagnostic(JSON.stringify({
    downloaded, target, globalInstallStatus: install.status,
    localInstallStatus: localInstall.status, cachedExecStatus: 23, argv,
  }));
});
