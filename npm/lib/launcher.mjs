// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { spawn } from "node:child_process";
import { readFileSync, realpathSync, statSync, accessSync, constants } from "node:fs";
import { createRequire } from "node:module";
import { constants as osConstants } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { binaryName, packageName, selectTarget } from "./platforms.mjs";

const require = createRequire(import.meta.url);
const manifest = JSON.parse(readFileSync(new URL("../package.json", import.meta.url)));
const wrapper = fileURLToPath(new URL("../bin/aiven-ai-gateway.mjs", import.meta.url));

export function resolveBinary() {
  const target = selectTarget(process.platform, process.arch, process.config.variables.arm_version);
  const name = packageName(target);
  let metadataPath;
  try {
    metadataPath = require.resolve(`${name}/package.json`);
  } catch {
    throw new Error(`native package ${name} is missing; reinstall @aiven/ai-gateway with optional dependencies enabled (do not use --omit=optional)`);
  }
  let native;
  try {
    native = JSON.parse(readFileSync(metadataPath, "utf8"));
    if (!native || typeof native !== "object") throw new Error();
  } catch {
    throw new Error("invalid native package metadata; reinstall @aiven/ai-gateway");
  }
  if (native.name !== name || native.version !== manifest.version ||
      manifest.optionalDependencies[name] !== manifest.version ||
      native.os?.length !== 1 || native.os[0] !== target.os ||
      native.cpu?.length !== 1 || native.cpu[0] !== target.cpu) {
    throw new Error("native package identity, version or platform mismatch; reinstall @aiven/ai-gateway");
  }
  let binary;
  try {
    binary = realpathSync(join(dirname(metadataPath), "bin", binaryName(target)));
    if (!statSync(binary).isFile() || binary === realpathSync(wrapper)) throw new Error();
    accessSync(binary, process.platform === "win32" ? constants.F_OK : constants.X_OK);
  } catch {
    throw new Error("native binary is missing, not executable or points to the npm wrapper; reinstall @aiven/ai-gateway");
  }
  return binary;
}

export function run(argv) {
  let binary;
  try {
    binary = resolveBinary();
  } catch (err) {
    // Only our fixed diagnostic text is exposed; parser/path errors may contain input.
    process.stderr.write(`aiven-ai-gateway: ${err.message}\n`);
    process.exitCode = 1;
    return;
  }
  const cannotStart = () => {
    process.stderr.write("aiven-ai-gateway: cannot start native binary; reinstall @aiven/ai-gateway\n");
    process.exitCode = 1;
  };
  let child;
  try {
    child = spawn(binary, argv, { stdio: "inherit", shell: false });
  } catch {
    // Node throws, rather than emitting "error", for any spawn failure other than EACCES, EAGAIN, EMFILE, ENFILE and
    // ENOENT; Windows reports a file that is not a valid executable that way.
    cannotStart();
    return;
  }
  // A terminal sends Ctrl-C to the whole foreground process group, so the tool already has its SIGINT and a forwarded
  // copy would be a second interrupt. Windows delivers Ctrl-C and console close to every process on the console, and
  // child.kill would terminate the tool there instead. In both cases the wrapper only waits for the tool to exit.
  const forwarded = process.platform === "win32" ? [] : ["SIGTERM", "SIGHUP"];
  const handlers = new Map();
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
    const handler = forwarded.includes(signal) ? () => child.kill(signal) : () => {};
    handlers.set(signal, handler);
    process.on(signal, handler);
  }
  const cleanup = () => {
    for (const [signal, handler] of handlers) process.removeListener(signal, handler);
  };
  child.once("error", () => {
    cleanup();
    cannotStart();
  });
  child.once("exit", (code, signal) => {
    cleanup();
    if (signal && process.platform !== "win32") {
      // Node ignores SIGPIPE, so re-raising it returns and would exit 0; the shell's status for the signal stands in.
      process.exitCode = 128 + osConstants.signals[signal];
      process.kill(process.pid, signal);
    } else {
      process.exitCode = code ?? 1;
    }
  });
}
