// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

import assert from "node:assert/strict";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, test } from "node:test";
import { distTag, integrity, publishPackages } from "../scripts/publish.mjs";
import { workspace } from "./support.mjs";

after(() => rmSync(workspace, { recursive: true, force: true, maxRetries: 3 }));
const directory = join(workspace, "publish");
mkdirSync(directory);
const names = ["@aivenio/ai-gateway-linux-x64", "@aivenio/ai-gateway-darwin-arm64", "@aivenio/ai-gateway"];
function packages(version = "1.2.3") {
  return names.map((name, index) => {
    const file = join(directory, `${index}-${version}.tgz`);
    writeFileSync(file, `${name}@${version}`);
    return { name, version, file };
  });
}
// A registry double. `published` maps name@version to an integrity, `view` replaces every lookup's result, and
// `rejects` is a tarball whose publish fails. An absent package or version is npm's exit 1 with a JSON E404.
function registry({ published = {}, view, rejects } = {}) {
  const calls = [];
  const npm = (args) => {
    calls.push(args);
    if (args[0] === "view") {
      if (view) return view;
      const found = published[args[1]];
      return found
        ? { status: 0, stdout: `${JSON.stringify(found)}\n`, stderr: "" }
        : { status: 1, stdout: '{"error":{"code":"E404"}}', stderr: "npm error code E404" };
    }
    if (args[1] === rejects) return { status: 1, stdout: "", stderr: "npm error code E403" };
    return { status: 0, stdout: "", stderr: "" };
  };
  return { npm, publishes: () => calls.filter((args) => args[0] === "publish") };
}

test("publishes every package in the given order, public, with provenance, as latest", () => {
  const list = packages();
  const { npm, publishes } = registry();
  const done = publishPackages(list, { npm });
  assert.deepEqual(publishes(), list.map(({ file }) => ["publish", file, "--access", "public", "--provenance", "--tag", "latest"]));
  assert.deepEqual(done, names.map((name) => ({ name, action: "published" })));
});

test("a prerelease is published as next and never as latest", () => {
  const { npm, publishes } = registry();
  publishPackages(packages("1.2.3-rc.1"), { npm });
  for (const args of publishes()) assert.deepEqual(args.slice(-2), ["--tag", "next"]);
  assert.equal(distTag("0.2.0"), "latest");
  assert.equal(distTag("1.0.0-0"), "next");
});

test("a package already published with the same bytes is skipped", () => {
  const list = packages();
  const { npm, publishes } = registry({ published: { [`${names[0]}@1.2.3`]: integrity(readFileSync(list[0].file)) } });
  const done = publishPackages(list, { npm });
  assert.deepEqual(publishes().map((args) => args[1]), [list[1].file, list[2].file]);
  assert.deepEqual(done[0], { name: names[0], action: "already published" });
});

test("a different build of a published version fails and publishes nothing further", () => {
  const list = packages();
  const { npm, publishes } = registry({ published: { [`${names[1]}@1.2.3`]: integrity(Buffer.from("other bytes")) } });
  assert.throws(() => publishPackages(list, { npm }), (error) => {
    assert.match(error.message, /@aivenio\/ai-gateway-darwin-arm64@1\.2\.3: a different build/);
    assert.match(error.message, /not attempted: \["@aivenio\/ai-gateway"\]/);
    return true;
  });
  assert.deepEqual(publishes().map((args) => args[1]), [list[0].file]);
});

test("a failed publish stops before the wrapper", () => {
  const list = packages();
  const { npm, publishes } = registry({ rejects: list[1].file });
  assert.throws(() => publishPackages(list, { npm }), /ai-gateway-darwin-arm64@1\.2\.3: publish failed: npm error code E403/);
  assert.deepEqual(publishes().map((args) => args[1]), [list[0].file, list[1].file]);
});

test("a published version without an integrity fails", () => {
  const { npm, publishes } = registry({ view: { status: 0, stdout: "", stderr: "" } });
  assert.throws(() => publishPackages(packages(), { npm }), /registry lookup failed/);
  assert.equal(publishes().length, 0);
});

test("a failed registry lookup publishes nothing", () => {
  const { npm, publishes } = registry({ view: { status: 1, stdout: "", stderr: "npm error code ECONNRESET" } });
  assert.throws(() => publishPackages(packages(), { npm }), /registry lookup failed: npm error code ECONNRESET/);
  assert.equal(publishes().length, 0);
});

test("a non-404 error that mentions E404 publishes nothing", () => {
  const stdout = JSON.stringify({ error: { code: "E503", summary: "upstream answered E404" } });
  const { npm, publishes } = registry({ view: { status: 1, stdout, stderr: "npm error code E503" } });
  assert.throws(() => publishPackages(packages(), { npm }), /registry lookup failed: npm error code E503/);
  assert.equal(publishes().length, 0);
});

test("a dry run looks up every package and publishes none", () => {
  const { npm, publishes } = registry();
  const done = publishPackages(packages(), { npm, dryRun: true });
  assert.equal(publishes().length, 0);
  assert.deepEqual(done, names.map((name) => ({ name, action: "would publish" })));
});

test("a timed-out lookup or publish names the timeout", () => {
  const timeout = { status: null, stdout: "", stderr: "", error: new Error("spawnSync node ETIMEDOUT") };
  assert.throws(() => publishPackages(packages(), { npm: registry({ view: timeout }).npm }), /registry lookup failed: spawnSync node ETIMEDOUT/);
  const { npm } = registry();
  const slow = (args) => (args[0] === "publish" ? timeout : npm(args));
  assert.throws(() => publishPackages(packages(), { npm: slow }), /publish failed: spawnSync node ETIMEDOUT/);
});
