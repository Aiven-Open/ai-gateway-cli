// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { test } from "node:test";
import { checkNoticeInventory } from "../scripts/notices.mjs";

const notices = "Component: Go go1.27.3\nComponent: example.test/library v1.2.3\n";
test("notice inventory accepts the linked component set, including repeated packages", () => {
  checkNoticeInventory(notices, "go1.27.3", ["example.test/library v1.2.3", "example.test/library v1.2.3"]);
});

for (const [name, text, toolchain, modules] of [
  ["version bump", notices, "go1.27.3", ["example.test/library v1.2.4"]],
  ["new dependency", notices, "go1.27.3", ["example.test/library v1.2.3", "example.test/new v1.0.0"]],
  ["removed dependency", notices, "go1.27.3", []],
  ["replacement", notices, "go1.27.3", ["example.test/library v1.2.3 replaced"]],
  ["toolchain bump", notices, "go1.28.0", ["example.test/library v1.2.3"]],
  ["missing inventory", "", "go1.27.3", ["example.test/library v1.2.3"]],
  ["duplicate component", notices + "Component: example.test/library v1.2.3\n", "go1.27.3", ["example.test/library v1.2.3"]],
]) {
  test(`notice inventory rejects ${name}`, () => {
    assert.throws(() => checkNoticeInventory(text, toolchain, modules));
  });
}
