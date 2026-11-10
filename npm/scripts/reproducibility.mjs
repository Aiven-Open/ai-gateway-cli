// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { join } from "node:path";
import { binaryName, targets } from "../lib/platforms.mjs";
import { inspectArtifacts, readTarball, sha256 } from "./artifacts.mjs";

export function compareTargetBuilds(first, second) {
  const inputs = ({ sha256: ignored, ...provenance }) => provenance;
  assert.deepEqual(inputs(first.provenance), inputs(second.provenance), "build inputs differ");
  assert.equal(sha256(first.bytes), sha256(second.bytes), "binary bytes differ");
  return sha256(first.bytes);
}

export function compareBuilds(first, second) {
  const firstSummary = inspectArtifacts(join(first, "artifacts"));
  const secondSummary = inspectArtifacts(join(second, "artifacts"));
  return targets.map((target, index) => {
    const read = (directory, summary) => {
      const entries = readTarball(join(directory, "artifacts", summary[index].archive));
      return {
        bytes: entries.get(`package/bin/${binaryName(target)}`).bytes,
        provenance: JSON.parse(entries.get("package/provenance.json").bytes.toString()),
      };
    };
    return { target, sha256: compareTargetBuilds(read(first, firstSummary), read(second, secondSummary)) };
  });
}
