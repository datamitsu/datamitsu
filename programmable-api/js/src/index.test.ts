import assert from "node:assert/strict";
import { describe, it } from "node:test";

describe("index module exports", () => {
  it("named imports work for all commands", async () => {
    const { cache, check, exec, fix, lint, parseToolList, version } = await import("./index.js");
    assert.equal(typeof fix, "function");
    assert.equal(typeof lint, "function");
    assert.equal(typeof check, "function");
    assert.equal(typeof exec, "function");
    assert.equal(typeof parseToolList, "function");
    assert.equal(typeof cache, "object");
    assert.equal(typeof cache.clear, "function");
    assert.equal(typeof cache.path, "function");
    assert.equal(typeof cache.pathProject, "function");
    assert.equal(typeof version, "function");
  });

  it("default import contains all methods", async () => {
    const { default: datamitsu } = await import("./index.js");
    assert.equal(typeof datamitsu, "object");
    assert.equal(typeof datamitsu.fix, "function");
    assert.equal(typeof datamitsu.lint, "function");
    assert.equal(typeof datamitsu.check, "function");
    assert.equal(typeof datamitsu.exec, "function");
    assert.equal(typeof datamitsu.cache, "object");
    assert.equal(typeof datamitsu.cache.clear, "function");
    assert.equal(typeof datamitsu.cache.path, "function");
    assert.equal(typeof datamitsu.cache.pathProject, "function");
    assert.equal(typeof datamitsu.version, "function");
  });

  it("named exports are the same references as default export methods", async () => {
    const mod = await import("./index.js");
    assert.equal(mod.fix, mod.default.fix);
    assert.equal(mod.lint, mod.default.lint);
    assert.equal(mod.check, mod.default.check);
    assert.equal(mod.exec, mod.default.exec);
    assert.equal(mod.cache, mod.default.cache);
    assert.equal(mod.version, mod.default.version);
  });
});
