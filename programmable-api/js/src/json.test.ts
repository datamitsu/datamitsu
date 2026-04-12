import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { extractAllJSON, extractJSON } from "./json.ts";

describe("extractJSON", () => {
  it("extracts JSON from output with prefix lines", () => {
    const output = 'info line 1\ninfo line 2\n{"operation": "fix", "groups": []}';
    const result = extractJSON(output);
    assert.equal(result, '{"operation": "fix", "groups": []}');
    assert.deepEqual(JSON.parse(result!), {
      groups: [],
      operation: "fix",
    });
  });

  it("returns the full string when it starts with {", () => {
    const output = '{"operation": "fix"}';
    const result = extractJSON(output);
    assert.equal(result, '{"operation": "fix"}');
  });

  it("returns null when no JSON object found", () => {
    const result = extractJSON("no json here");
    assert.equal(result, null);
  });

  it("returns null for empty string", () => {
    const result = extractJSON("");
    assert.equal(result, null);
  });

  it("handles multiline JSON with prefix", () => {
    const output = 'prefix\n{\n  "key": "value"\n}';
    const result = extractJSON(output);
    assert.equal(result, '{\n  "key": "value"\n}');
  });

  it("extracts first JSON object when multiple exist", () => {
    const output = '{"op": "fix"}\n{"op": "lint"}';
    const result = extractJSON(output);
    assert.equal(result, '{"op": "fix"}');
  });

  it("extracts first JSON object with text between them", () => {
    const output = 'prefix\n{"a": 1}\nsome text\n{"b": 2}';
    const result = extractJSON(output);
    assert.equal(result, '{"a": 1}');
  });

  it("handles nested braces correctly", () => {
    const output = '{"outer": {"inner": "value"}}';
    const result = extractJSON(output);
    assert.equal(result, '{"outer": {"inner": "value"}}');
  });

  it("returns null for unmatched opening brace", () => {
    const result = extractJSON("prefix { no close");
    assert.equal(result, null);
  });

  it("handles braces inside JSON string values", () => {
    const output = '{"args": ["{file}", "{root}/config"]}';
    const result = extractJSON(output);
    assert.equal(result, output);
    assert.deepEqual(JSON.parse(result!), { args: ["{file}", "{root}/config"] });
  });

  it("handles unbalanced braces inside JSON string values", () => {
    const output = '{"msg": "missing closing brace {"}';
    const result = extractJSON(output);
    assert.equal(result, output);
    assert.deepEqual(JSON.parse(result!), { msg: "missing closing brace {" });
  });

  it("handles escaped quotes inside JSON strings", () => {
    const output = String.raw`{"msg": "say \"hello\" {world}"}`;
    const result = extractJSON(output);
    assert.equal(result, output);
    assert.deepEqual(JSON.parse(result!), { msg: 'say "hello" {world}' });
  });
});

describe("extractAllJSON", () => {
  it("returns empty array for null/empty input", () => {
    assert.deepEqual(extractAllJSON(""), []);
  });

  it("returns single JSON object", () => {
    const result = extractAllJSON('{"a": 1}');
    assert.deepEqual(result, ['{"a": 1}']);
  });

  it("extracts multiple JSON objects", () => {
    const result = extractAllJSON('{"op": "fix"}\n{"op": "lint"}');
    assert.deepEqual(result, ['{"op": "fix"}', '{"op": "lint"}']);
  });

  it("extracts JSON objects with text between them", () => {
    const output = 'prefix\n{"a": 1}\nsome text\n{"b": 2}\ntrailing';
    const result = extractAllJSON(output);
    assert.deepEqual(result, ['{"a": 1}', '{"b": 2}']);
  });

  it("handles string-aware extraction for multiple objects", () => {
    const output = '{"msg": "{ hi }"}\n{"val": 2}';
    const result = extractAllJSON(output);
    assert.equal(result.length, 2);
    assert.deepEqual(JSON.parse(result[0]), { msg: "{ hi }" });
    assert.deepEqual(JSON.parse(result[1]), { val: 2 });
  });
});
