// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { gunzipSync } from "node:zlib";
import { fileURLToPath } from "node:url";
import { binaryName, packageName, targets } from "../lib/platforms.mjs";

export const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const repository = { type: "git", url: "https://github.com/aiven-open/ai-gateway-cli.git" };
const root = fileURLToPath(new URL("../../", import.meta.url));

export function readTarball(file) {
  const tar = gunzipSync(readFileSync(file));
  const entries = new Map();
  for (let offset = 0; offset + 512 <= tar.length;) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const text = (start, length) => header.subarray(start, start + length).toString().replace(/\0.*$/s, "");
    const name = text(0, 100);
    const size = parseInt(text(124, 12).trim(), 8);
    const mode = parseInt(text(100, 8).trim(), 8);
    assert(Number.isSafeInteger(size) && size >= 0 && offset + 512 + size <= tar.length, "truncated tar entry");
    assert(!entries.has(name), "duplicate tar entry");
    assert(["", "0"].includes(text(156, 1)), "packages must contain regular files only");
    entries.set(name, { mode, bytes: tar.subarray(offset + 512, offset + 512 + size) });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return entries;
}

export function inspectBinary(bytes, target) {
  if (target.os === "linux") {
    assert.equal(bytes.subarray(0, 4).toString("hex"), "7f454c46");
    assert.equal(bytes[4], target.cpu === "arm" ? 1 : 2);
    assert.equal(bytes[5], 1, "ELF must be little endian");
    assert.equal(bytes.readUInt16LE(18), { x64: 62, arm64: 183, arm: 40 }[target.cpu]);
    const is64 = bytes[4] === 2;
    const offset = is64 ? Number(bytes.readBigUInt64LE(32)) : bytes.readUInt32LE(28);
    const size = bytes.readUInt16LE(is64 ? 54 : 42);
    const count = bytes.readUInt16LE(is64 ? 56 : 44);
    for (let i = 0; i < count; i++) {
      assert.notEqual(bytes.readUInt32LE(offset + i * size), 3, "Linux binary must not need a libc interpreter");
    }
  } else if (target.os === "darwin") {
    assert.equal(bytes.subarray(0, 4).toString("hex"), "cffaedfe");
    assert.equal(bytes.readUInt32LE(4), target.cpu === "x64" ? 0x1000007 : 0x100000c);
  } else {
    assert.equal(bytes.subarray(0, 2).toString(), "MZ");
    const offset = bytes.readUInt32LE(60);
    assert.equal(bytes.subarray(offset, offset + 4).toString(), "PE\0\0");
    assert.equal(bytes.readUInt16LE(offset + 4), target.cpu === "x64" ? 0x8664 : 0xaa64);
  }
}

export function inspectArtifacts(directory) {
  const tarballs = readdirSync(directory).filter((file) => file.endsWith(".tgz"));
  assert.equal(tarballs.length, 8, "need main plus seven native tarballs");
  const packages = new Map();
  for (const file of tarballs) {
    const entries = readTarball(join(directory, file));
    const metadata = JSON.parse(entries.get("package/package.json")?.bytes.toString());
    assert.deepEqual(metadata.repository, repository, "package must identify the public source repository");
    assert.equal(metadata.license, "Apache-2.0");
    for (const file of ["LICENSE", "THIRD_PARTY_NOTICES"]) {
      const shipped = entries.get(`package/${file}`)?.bytes;
      assert(shipped?.equals(readFileSync(join(root, file))), `missing or changed ${file}`);
    }
    assert(!packages.has(metadata.name), "duplicate package");
    packages.set(metadata.name, { metadata, entries, file });
  }
  const main = packages.get("@aivenio/ai-gateway");
  assert(main, "missing main package");
  assert.deepEqual(main.metadata.bin, { "aiven-ai-gateway": "./bin/aiven-ai-gateway.mjs" });
  assert.deepEqual([...main.entries.keys()].sort(), [
    "package/LICENSE", "package/README.md", "package/THIRD_PARTY_NOTICES", "package/bin/aiven-ai-gateway.mjs",
    "package/lib/launcher.mjs", "package/lib/platforms.mjs", "package/package.json",
  ]);
  assert.equal(main.entries.get("package/bin/aiven-ai-gateway.mjs").mode & 0o111, 0o111);
  assert.equal(main.metadata.scripts?.install, undefined);
  assert.equal(main.metadata.scripts?.postinstall, undefined);
  const expectedDependencies = {};
  const summary = [];
  for (const target of targets) {
    const name = packageName(target);
    expectedDependencies[name] = main.metadata.version;
    const native = packages.get(name);
    assert(native, `missing ${name}`);
    assert.equal(native.metadata.version, main.metadata.version, "package version mismatch");
    assert.deepEqual(native.metadata.os, [target.os]);
    assert.deepEqual(native.metadata.cpu, [target.cpu]);
    assert.equal(native.metadata.dependencies, undefined);
    assert.equal(native.metadata.scripts, undefined, "native package must not run install hooks");
    const binaryPath = `package/bin/${binaryName(target)}`;
    assert.deepEqual([...native.entries.keys()].sort(), [
      binaryPath, "package/LICENSE", "package/THIRD_PARTY_NOTICES", "package/package.json", "package/provenance.json",
    ].sort(), "native package must ship exactly one binary");
    const binary = native.entries.get(binaryPath);
    assert(binary?.bytes.length > 0, "missing native binary");
    assert.equal(binary.mode & 0o111, 0o111, "native binary must be executable");
    inspectBinary(binary.bytes, target);
    const provenance = JSON.parse(native.entries.get("package/provenance.json").bytes.toString());
    assert.equal(provenance.version, main.metadata.version);
    assert.equal(provenance.sha256, sha256(binary.bytes));
    assert.equal(provenance.cgoEnabled, "0");
    assert.deepEqual(provenance.target, target);
    assert.match(provenance.commit, /^[a-f0-9]{40}$/);
    summary.push({
      name, version: native.metadata.version, os: target.os, cpu: target.cpu,
      archive: native.file, archiveBytes: readFileSync(join(directory, native.file)).length,
      binaryBytes: binary.bytes.length, sha256: provenance.sha256,
    });
  }
  assert.deepEqual(main.metadata.optionalDependencies, expectedDependencies);
  return summary;
}
