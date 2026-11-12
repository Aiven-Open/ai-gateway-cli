// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createServer } from "node:https";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { inspectArtifacts } from "./artifacts.mjs";
import { packageName, selectTarget } from "../lib/platforms.mjs";

assert.equal(process.argv.length, 4, "usage: smoke.mjs <prepared-directory> <new-isolated-directory>");
const prepared = resolve(process.argv[2]);
const isolated = resolve(process.argv[3]);
const target = selectTarget(process.platform, process.arch, process.config.variables.arm_version);
const summary = inspectArtifacts(join(prepared, "artifacts"));
const native = summary.find((item) => item.name === packageName(target));
const version = native.version;
const mainTarball = `aiven-ai-gateway-${version}.tgz`;
mkdirSync(isolated);
const home = join(isolated, "home");
mkdirSync(home);
const userconfig = join(home, "user-npmrc"), globalconfig = join(home, "global-npmrc");
writeFileSync(userconfig, "");
writeFileSync(globalconfig, "");
const prefix = join(isolated, "prefix");
const isWin = process.platform === "win32";
// Windows shells set USERPROFILE without HOME; ProgramFiles lets Pi's shell lookup find Git Bash before WSL's bash.exe.
const env = {
  PATH: process.env.PATH,
  ...(isWin
    ? {
        SystemRoot: process.env.SystemRoot,
        ProgramFiles: process.env.ProgramFiles,
        "ProgramFiles(x86)": process.env["ProgramFiles(x86)"],
        USERPROFILE: home,
        TEMP: home,
        TMP: home,
      }
    : { HOME: home, TMPDIR: home }),
  npm_config_cache: join(isolated, "cache"), npm_config_userconfig: userconfig,
  npm_config_globalconfig: globalconfig, npm_config_update_notifier: "false",
  npm_config_audit: "false", npm_config_fund: "false", npm_config_offline: "true",
};
const npmCLI = process.env.npm_execpath;
assert(npmCLI, "run through npm run smoke");
function run(args) {
  const result = spawnSync(process.execPath, args, { env, encoding: "utf8", timeout: 60_000, cwd: isolated });
  return { status: result.status, signal: result.signal, stdout: result.stdout, stderr: result.stderr, error: result.error?.message };
}
const installed = run([npmCLI, "install", "-g", "--prefix", prefix, "--offline", "--ignore-scripts",
  join(prepared, "artifacts", mainTarball), join(prepared, "artifacts", native.archive)]);
assert.equal(installed.status, 0, installed.stderr);
const globalRoot = isWin ? prefix : join(prefix, "lib");
const entry = join(globalRoot, "node_modules/@aiven/ai-gateway/bin/aiven-ai-gateway.mjs");
const bare = run([entry]);
assert.equal(bare.status, 0, bare.stderr);
assert.match(bare.stdout, /setup/);
assert.match(bare.stdout, /launch/);
const help = run([entry, "--help"]);
assert.equal(help.status, 0, help.stderr);
assert.equal(help.stdout, bare.stdout);
const invalid = run([entry, "--definitely-unknown"]);
assert.equal(invalid.status, 1);
const shim = isWin ? join(prefix, "aiven-ai-gateway.cmd") : join(prefix, "bin/aiven-ai-gateway");
const shimHelp = spawnSync(shim, ["--help"], {
  env, encoding: "utf8", timeout: 30_000, shell: isWin, cwd: isolated,
});
assert.equal(shimHelp.status, 0, shimHelp.stderr);
assert.equal(shimHelp.stdout, help.stdout);

// Setup and launch each tool against a loopback gateway, with a synthetic key and a fixture standing in for the tool.
const key = "sk-smoke-synthetic";
const catalogue = [
  { id: "claude-sonnet", owned_by: "anthropic", context_window_tokens: 1000000, max_output_tokens: 64000, input_usd_per_million_tokens: 3, output_usd_per_million_tokens: 15 },
  { id: "claude-opus", owned_by: "anthropic", context_window_tokens: 1000000, input_usd_per_million_tokens: 15, output_usd_per_million_tokens: 75 },
  { id: "gpt-sol", owned_by: "OpenAI", context_window_tokens: 400000, input_usd_per_million_tokens: 1.25, output_usd_per_million_tokens: 10 },
  { id: "glm", owned_by: "zai", context_window_tokens: 1310720, input_usd_per_million_tokens: 0.5, output_usd_per_million_tokens: 2 },
].map((model) => ({ object: "model", created: 0, ...model }));
const answers = {
  "/v1/chat/completions": { id: "smoke", choices: [] },
  "/v1/messages": { id: "smoke", type: "message", content: [] },
  "/v1/responses": { id: "smoke", object: "response", status: "completed", error: null, output: [] },
};
const tools = join(isolated, "tools");
mkdirSync(tools, { recursive: true });
const repository = fileURLToPath(new URL("../../", import.meta.url));
const fixtureBin = join(tools, isWin ? "fixture.exe" : "fixture");
const built = spawnSync("go", ["build", "-o", fixtureBin, "./npm/test/fixture"], {
  cwd: repository, encoding: "utf8", timeout: 300_000,
});
assert.equal(built.status, 0, built.stderr);
const generated = spawnSync(fixtureBin, [], {
  cwd: isolated, env: { ...env, AIVEN_NPM_TEST_CERTIFICATE: "1" }, encoding: "utf8", timeout: 30_000,
});
assert.equal(generated.status, 0, generated.stderr);
const certificate = JSON.parse(generated.stdout);
const trustDir = join(isolated, "tls-trust");
mkdirSync(trustDir);
process.once("exit", () => rmSync(trustDir, { recursive: true, force: true }));
const caFile = join(trustDir, "ca.pem");
writeFileSync(caFile, certificate.CA, { mode: 0o600 });
const requests = [];
const server = createServer({ key: certificate.PrivateKey, cert: certificate.Certificate }, (request, response) => {
  requests.push(`${request.method} ${request.url}`);
  const body = request.method === "GET" && request.url === "/v1/models"
    ? { object: "list", data: catalogue } : request.method === "POST" && answers[request.url];
  if (request.headers.authorization !== `Bearer ${key}`) response.writeHead(401).end('{"error":{"message":"Authentication Error"}}');
  else if (!body) response.writeHead(404).end();
  else response.setHeader("content-type", "application/json").end(JSON.stringify(body));
  request.resume();
});
server.listen(0, "127.0.0.1");
await once(server, "listening");
const names = ["claude", "codex", "pi", "opencode", "omp"];
for (const name of names) {
  if (isWin && name !== "codex") {
    // npm-installed tools on Windows are cmd-shim .cmd wrappers; codex exercises direct .exe handoff beside them.
    const cmdShim = "@ECHO off\r\n" +
      "GOTO start\r\n" +
      ":find_dp0\r\n" +
      "SET dp0=%~dp0\r\n" +
      "EXIT /b\r\n" +
      ":start\r\n" +
      "SETLOCAL\r\n" +
      "CALL :find_dp0\r\n" +
      "SET \"_prog=%dp0%\\fixture.exe\"\r\n" +
      "endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\" %*\r\n";
    writeFileSync(join(tools, `${name}.cmd`), cmdShim);
  } else {
    copyFileSync(fixtureBin, join(tools, isWin ? `${name}.exe` : name));
  }
}
const toolEnv = {
  ...env, PATH: [tools, process.env.PATH].join(isWin ? ";" : ":"),
  AIVEN_AI_GATEWAY_BASE_URL: `https://127.0.0.1:${server.address().port}`,
  SSL_CERT_FILE: caFile, SSL_CERT_DIR: trustDir,
  AIVEN_AI_GATEWAY_KEY: "sk-smoke-inherited-other", ANTHROPIC_AUTH_TOKEN: "sk-smoke-inherited-anthropic",
  AIVEN_NPM_TEST_CAPTURE_AUTH: "1",
  AIVEN_NPM_TEST_ENV: "from-launch", AIVEN_NPM_TEST_STATUS: "23",
};
function runAsync(args, input) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [entry, ...args], { env: toolEnv, cwd: isolated });
    let stdout = "", stderr = "";
    const timer = setTimeout(() => { child.kill(); reject(new Error(`${args.join(" ")} timed out`)); }, 60_000);
    child.stdout.on("data", (data) => { stdout += data; });
    child.stderr.on("data", (data) => { stderr += data; });
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("close", (status) => { clearTimeout(timer); resolve({ status, stdout, stderr }); });
    child.stdin.end(input);
  });
}
function files(directory) {
  return readdirSync(directory, { recursive: true }).map((file) => join(directory, file))
    .filter((file) => statSync(file).isFile());
}
// The files each setup writes and the arguments each launch puts before the native ones; a model flag goes in only
// when there are none.
const expected = {
  claude: {
    files: [".claude/aiven.settings.json"],
    argv: (a) => {
      if (a.length !== 2 || a[0] !== "--settings") return false;
      const parsed = JSON.parse(a[1]);
      return parsed.env?.ANTHROPIC_BASE_URL === toolEnv.AIVEN_AI_GATEWAY_BASE_URL && typeof parsed.apiKeyHelper === "string";
    },
  },
  codex: { files: [".codex/aiven.config.toml", ".codex/config.toml"], argv: (a) => a.join(" ") === "-p aiven" },
  pi: { files: [".pi/agent/models.json"], argv: (a) => a.length === 0 },
  opencode: { files: [".config/opencode/opencode.json"], argv: (a) => a.length === 0 },
  omp: { files: [".omp/agent/models.yml"], argv: (a) => a.length === 0 },
};
const launches = {};
for (const name of names) {
  const before = new Set(files(home));
  const setup = await runAsync(["setup", name, "--key-stdin", "--yes", "--plain"], `${key}\n`);
  assert.equal(setup.status, 0, `${name} setup: ${setup.stdout}${setup.stderr}`);
  assert(setup.stderr.includes(`Gateway: ${toolEnv.AIVEN_AI_GATEWAY_BASE_URL}\n`), `${name} setup omitted the gateway`);
  const written = files(home).filter((file) => !before.has(file) && !file.includes("ai-gateway-key"))
    .map((file) => file.slice(home.length + 1).replaceAll("\\", "/")).sort();
  assert.deepEqual(written, expected[name].files, `${name} setup wrote ${written}`);
  for (const file of written) {
    const text = readFileSync(join(home, file), "utf8");
    assert(text.includes(toolEnv.AIVEN_AI_GATEWAY_BASE_URL), `${file} does not point at the gateway`);
    assert(!text.includes(key), `${name} setup wrote the key into ${file}`);
  }
  const argv = ["two words", "", 'quote"slash\\', "a & b | c < d > e (f) ^ %USERPROFILE%", '{"inline":"x && y || z"}'];
  const launch = await runAsync(["launch", name, "--", ...argv], "");
  assert.equal(launch.status, 23, `${name} launch: ${launch.stderr}`);
  assert(launch.stderr.includes(`Gateway: ${toolEnv.AIVEN_AI_GATEWAY_BASE_URL}\n`), `${name} launch omitted the gateway`);
  const observed = JSON.parse(launch.stdout.trim().split("\n").at(-1));
  assert.deepEqual(observed.Argv.slice(-argv.length), argv, `${name} native arguments`);
  assert(expected[name].argv(observed.Argv.slice(0, -argv.length)), `${name} launch arguments`);
  assert(!JSON.stringify(observed.Argv).includes(key), `${name} launch put the key in argv`);
  assert.equal(observed.Env, "from-launch");
  assert.equal(observed.GatewayKeySet, false, `${name} inherited an unbound gateway key`);
  assert.equal(observed.GatewayKey, "", `${name} received a gateway key in its environment`);
  assert.equal(observed.AnthropicToken, name === "claude" ? "" : "sk-smoke-inherited-anthropic");
  launches[name] = { argv: observed.Argv.map((arg) => arg.slice(0, 40)), written };
}
const modelList = await runAsync(["models"], "");
assert.equal(modelList.status, 0, modelList.stderr);
assert(modelList.stderr.startsWith(`Gateway: ${toolEnv.AIVEN_AI_GATEWAY_BASE_URL}\n`), "models omitted the gateway");
assert(modelList.stdout.includes("claude-sonnet") && !modelList.stdout.includes("Gateway:"), "models stdout changed");
server.close();
// Another gateway's key file is named as docs/reference.md says: a slug of the URL, then 12 hex digits of its SHA-256.
const origin = toolEnv.AIVEN_AI_GATEWAY_BASE_URL;
const slug = origin.replace(/[^a-z0-9.-]+/g, "-").replace(/^[.-]+|[.-]+$/g, "").slice(0, 40).replace(/[.-]+$/, "");
const keyName = `${slug}-${createHash("sha256").update(origin).digest("hex").slice(0, 12)}`;
const keyPath = join(home, ".config/aiven/ai-gateway-keys", keyName);
assert.equal(readFileSync(keyPath, "utf8"), `${key}\n`);
assert(!existsSync(join(home, ".config/aiven/ai-gateway-key")), "setup wrote the default gateway's key file");

// Run each tool's generated key helper the way the tool runs it: with the key file present, then without it, when it
// must fail rather than print the environment's key, which names no gateway.
const claudeHelper = JSON.parse(readFileSync(join(home, ".claude/aiven.settings.json"), "utf8")).apiKeyHelper;
const piHelper = JSON.parse(readFileSync(join(home, ".pi/agent/models.json"), "utf8")).providers.aiven.apiKey.slice(1);
const gitBash = isWin
  ? [process.env.ProgramFiles, process.env["ProgramFiles(x86)"]].filter(Boolean)
      .map((dir) => join(dir, "Git/bin/bash.exe")).find((path) => existsSync(path)) || "bash.exe"
  : "/bin/sh";
const helperEnv = { ...toolEnv, AIVEN_AI_GATEWAY_KEY: "sk-env-unused" };
for (const want of [key, "sk-smoke-replacement", ""]) {
  if (want === "sk-smoke-replacement") writeFileSync(keyPath, `${want}\n`, { mode: 0o600 });
  if (want === "") rmSync(keyPath);
  const ok = (out) => (want === "" ? out.status !== 0 : out.status === 0);
  const claudeOut = spawnSync(claudeHelper, { shell: true, env: helperEnv, encoding: "utf8", timeout: 15_000 });
  assert(ok(claudeOut), `claude helper (key file ${want ? "present" : "absent"}): ${claudeOut.error ?? ""} ${claudeOut.stderr}`);
  assert.equal(claudeOut.stdout.trim(), want, "claude helper output");
  const ompOut = spawnSync(gitBash, ["-c", claudeHelper], { env: helperEnv, encoding: "utf8", timeout: 15_000 });
  assert(ok(ompOut), `omp bash-shell helper (key file ${want ? "present" : "absent"}): ${ompOut.error ?? ""} ${ompOut.stderr}`);
  assert.equal(ompOut.stdout.trim(), want, "omp helper output");
  const piOut = spawnSync(gitBash, ["-c", piHelper], { env: helperEnv, encoding: "utf8", timeout: 15_000 });
  assert(ok(piOut), `pi helper (key file ${want ? "present" : "absent"}): ${piOut.error ?? ""} ${piOut.stderr}`);
  assert.equal(piOut.stdout.trim(), want, "pi helper output");
}

assert(requests.some((request) => request === "POST /v1/messages"), "no Messages check reached the gateway");
assert(requests.some((request) => request === "POST /v1/responses"), "no Responses check reached the gateway");

const receipt = {
  target, version, native, install: installed, bare, help, invalid,
  shim: { status: shimHelp.status, stdout: shimHelp.stdout, stderr: shimHelp.stderr },
  launches, requests: requests.length,
};
writeFileSync(join(isolated, "receipt.json"), `${JSON.stringify(receipt, null, 2)}\n`);
process.stdout.write(`${JSON.stringify(receipt, null, 2)}\n`);
