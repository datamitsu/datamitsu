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
    const module_ = await import("./index.js");
    assert.equal(module_.fix, module_.default.fix);
    assert.equal(module_.lint, module_.default.lint);
    assert.equal(module_.check, module_.default.check);
    assert.equal(module_.exec, module_.default.exec);
    assert.equal(module_.cache, module_.default.cache);
    assert.equal(module_.version, module_.default.version);
  });
});
