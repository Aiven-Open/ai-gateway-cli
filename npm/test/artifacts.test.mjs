// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import { after, test } from "node:test";
import assert from "node:assert/strict";
import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { inspectArtifacts } from "../scripts/artifacts.mjs";
import { pack, workspace } from "./support.mjs";

const prepared = process.env.AIVEN_NPM_PREPARED;
assert(prepared, "AIVEN_NPM_PREPARED must name a prepare-packages output directory");
after(() => rmSync(workspace, { recursive: true, force: true, maxRetries: 3 }));

test("all eight packages identify their source and licenses; seven contain one executable and provenance", () => {
  assert.equal(inspectArtifacts(join(prepared, "artifacts")).length, 7);
});

for (const fault of [
  "missing-binary", "extra-binary", "wrong-version", "wrong-cpu", "wrong-hash", "wrong-header",
  "missing-repository", "forbidden-repository", "missing-license", "changed-license", "missing-notices", "changed-notices",
]) {
  test(`artifact inspection rejects ${fault}`, () => {
    const directory = join(workspace, fault);
    const artifacts = join(directory, "artifacts");
    mkdirSync(artifacts, { recursive: true });
    for (const file of readdirSync(join(prepared, "artifacts"))) {
      if (!file.includes("linux-x64")) cpSync(join(prepared, "artifacts", file), join(artifacts, file));
    }
    const native = join(directory, "native");
    cpSync(join(prepared, "packages/linux-x64"), native, { recursive: true });
    const binary = join(native, "bin/aiven-ai-gateway");
    if (fault === "missing-binary") rmSync(binary);
    if (fault === "extra-binary") cpSync(binary, join(native, "bin/aiven-ai-gateway.exe"));
    if (fault === "wrong-header") writeFileSync(binary, "not an executable");
    if (["wrong-version", "wrong-cpu", "missing-repository", "forbidden-repository"].includes(fault)) {
      const file = join(native, "package.json");
      const metadata = JSON.parse(readFileSync(file));
      if (fault === "wrong-version") metadata.version = "99.0.0";
      if (fault === "wrong-cpu") metadata.cpu = ["arm64"];
      if (fault === "missing-repository") delete metadata.repository;
      if (fault === "forbidden-repository") metadata.repository.url = "https://example.invalid/private-export-marker.git";
      writeFileSync(file, JSON.stringify(metadata));
    }
    if (fault === "missing-license") rmSync(join(native, "LICENSE"));
    if (fault === "changed-license") writeFileSync(join(native, "LICENSE"), "placeholder license");
    if (fault === "missing-notices") rmSync(join(native, "THIRD_PARTY_NOTICES"));
    if (fault === "changed-notices") writeFileSync(join(native, "THIRD_PARTY_NOTICES"), "placeholder notices");
    if (fault === "wrong-hash") {
      const file = join(native, "provenance.json");
      const provenance = JSON.parse(readFileSync(file));
      provenance.sha256 = "0".repeat(64);
      writeFileSync(file, JSON.stringify(provenance));
    }
    pack(native, artifacts);
    assert.throws(() => inspectArtifacts(artifacts));
  });
}

for (const fault of ["missing-repository", "forbidden-repository", "missing-license", "changed-license", "missing-notices", "changed-notices"]) {
  test(`main package inspection rejects ${fault}`, () => {
    const directory = join(workspace, `main-${fault}`);
    const artifacts = join(directory, "artifacts");
    mkdirSync(artifacts, { recursive: true });
    for (const file of readdirSync(join(prepared, "artifacts"))) {
      if (file !== `aivenio-ai-gateway-${JSON.parse(readFileSync(join(prepared, "packages/main/package.json"))).version}.tgz`) {
        cpSync(join(prepared, "artifacts", file), join(artifacts, file));
      }
    }
    const main = join(directory, "main");
    cpSync(join(prepared, "packages/main"), main, { recursive: true });
    const file = join(main, "package.json");
    const metadata = JSON.parse(readFileSync(file));
    if (fault === "missing-repository") delete metadata.repository;
    if (fault === "forbidden-repository") metadata.repository.url = "https://example.invalid/private-export-marker.git";
    writeFileSync(file, JSON.stringify(metadata));
    if (fault === "missing-license") rmSync(join(main, "LICENSE"));
    if (fault === "changed-license") writeFileSync(join(main, "LICENSE"), "placeholder license");
    if (fault === "missing-notices") rmSync(join(main, "THIRD_PARTY_NOTICES"));
    if (fault === "changed-notices") writeFileSync(join(main, "THIRD_PARTY_NOTICES"), "placeholder notices");
    pack(main, artifacts);
    assert.throws(() => inspectArtifacts(artifacts));
  });
}
