// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { test } from "node:test";
import { compareTargetBuilds } from "../scripts/reproducibility.mjs";
import { targets } from "../lib/platforms.mjs";

for (const target of targets) {
  test(`reproducibility compares actual bytes and build inputs for ${target.os}/${target.cpu}`, () => {
    const first = {
      bytes: Buffer.from("synthetic binary"),
      provenance: { version: "1.0.0", sourceDateEpoch: "100", target, sha256: "untrusted metadata" },
    };
    assert.match(compareTargetBuilds(first, structuredClone(first)), /^[a-f0-9]{64}$/);
    const changedBytes = { ...first, bytes: Buffer.from("different synthetic binary") };
    assert.throws(() => compareTargetBuilds(first, changedBytes), /binary bytes differ/);
    const changedInput = { ...first, provenance: { ...first.provenance, sourceDateEpoch: "101" } };
    assert.throws(() => compareTargetBuilds(first, changedInput), /build inputs differ/);
  });
}
