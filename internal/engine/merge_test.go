package engine

import (
	"testing"

	"github.com/dop251/goja"
)

func TestMergeSettings(t *testing.T) {
	e := &Engine{vm: goja.New()}
	e.initMerge()
	_, err := e.vm.RunString(`
const base = { rules: { a: 1, b: 2 }, list: [{ x: 1 }], enabled: true, count: 5 };
const overrides = { rules: { b: 0 }, list: [{ y: 2 }], enabled: false, nullable: null };
const result = merge(base, overrides, { extra: "yes" });
if (JSON.stringify(result) !== '{"rules":{"a":1,"b":0},"list":[{"y":2}],"enabled":false,"count":5,"nullable":null,"extra":"yes"}') throw Error("merge values");
result.rules.a = 9; result.list[0].y = 8;
if (base.rules.a !== 1 || overrides.list[0].y !== 2) throw Error("input mutated");
const unsafe = merge(JSON.parse('{"__proto__":{"polluted":true},"constructor":{"prototype":{"polluted":true}}}'));
if (({}).polluted !== undefined || unsafe.__proto__.polluted !== true) throw Error("prototype changed");
`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMergeRejectsInvalidInputs(t *testing.T) {
	for _, script := range []string{
		`merge(null)`, `merge([])`, `merge("x")`,
		`const a = {}; a.self = a; merge(a)`,
		`const a = []; a.push(a); merge({a})`,
	} {
		e := &Engine{vm: goja.New()}
		e.initMerge()
		if _, err := e.vm.RunString(script); err == nil {
			t.Errorf("expected rejection for %s", script)
		}
	}
}
