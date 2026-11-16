// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { join } from "node:path";
import { after, test } from "node:test";
import { rmSync } from "node:fs";
import { run, workspace } from "./support.mjs";

after(() => rmSync(workspace, { recursive: true, force: true, maxRetries: 3 }));

// npm normalises 1.2.3-01 to 1.2.3-1, which would publish a version that differs from the tag.
for (const version of ["1.2", "next", "1.2.3-01", "1.2.3-rc..1", "1.2.3-"]) {
  test(`preparation refuses version ${version} before building`, () => {
    const result = run(process.execPath, ["scripts/prepare.mjs", "--out", join(workspace, "never"), "--version", version]);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /version must be an explicit semver/);
  });
}
