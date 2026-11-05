// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { targets } from "../lib/platforms.mjs";

export function checkNoticeInventory(notices, toolchain, modules) {
  const components = [...notices.matchAll(/^Component: (\S+) (\S+)$/gm)];
  assert(components.length > 1, "missing third-party notice inventory");
  const inventory = components.map(([, name, version]) => `${name} ${version}`);
  assert.equal(new Set(inventory.map((entry) => entry.split(" ")[0])).size, inventory.length, "duplicate notice component");
  assert.deepEqual(inventory.sort(), [`Go ${toolchain}`, ...new Set(modules)].sort(), "linked components changed; review THIRD_PARTY_NOTICES");
}

export function checkLinkedNotices(root, run) {
  const notices = readFileSync(join(root, "THIRD_PARTY_NOTICES"), "utf8");
  const toolchain = run("go", ["env", "GOVERSION"]);
  for (const target of targets) {
    const modules = run("go", [
      "list", "-deps", "-f",
      "{{if and .Module (not .Module.Main)}}{{.Module.Path}} {{.Module.Version}}{{if .Module.Replace}} replaced{{end}}{{end}}",
      "./cmd/aiven-ai-gateway",
    ], root, { CGO_ENABLED: "0", GOOS: target.goos, GOARCH: target.goarch, GOARM: target.goarm ?? "" });
    checkNoticeInventory(notices, toolchain, modules.split("\n").filter(Boolean));
  }
}
