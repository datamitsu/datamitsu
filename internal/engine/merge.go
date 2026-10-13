package engine

func (e *Engine) initMerge() {
	_, err := e.vm.RunString(`
globalThis.merge = (...sources) => {
  const isRecord = value => value !== null && typeof value === "object" &&
    (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null);
  const visiting = new Set();
  const clone = value => {
    if (!isRecord(value) && !Array.isArray(value)) return value;
    if (visiting.has(value)) throw new TypeError("merge cannot copy cyclic settings");
    visiting.add(value);
    const result = Array.isArray(value) ? value.map(clone) : combine(Object.create(null), value);
    visiting.delete(value);
    return result;
  };
  const combine = (target, source) => {
    for (const key of Object.keys(source)) {
      const value = source[key];
      if (isRecord(target[key]) && isRecord(value)) {
        if (visiting.has(value)) throw new TypeError("merge cannot copy cyclic settings");
        visiting.add(value);
        combine(target[key], value);
        visiting.delete(value);
      } else target[key] = clone(value);
    }
    return target;
  };
  const result = Object.create(null);
  for (const source of sources) {
    if (!isRecord(source)) throw new TypeError("merge expects plain objects");
    visiting.add(source);
    combine(result, source);
    visiting.delete(source);
  }
  return result;
};`)
	if err != nil {
		panic(err)
	}
}
