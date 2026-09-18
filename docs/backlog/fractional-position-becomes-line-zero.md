---
worth: yes
where: parsers/datamitsu-parsers/src/numconv.rs:15
added: 2026-09-18
---

# A fractional line number becomes line 0 instead of no position

`json_u32` rejects a non-finite, negative or out-of-range number, but not a fractional one:

```rust
if n.is_finite() && n >= 0.0 && n <= u32::MAX as f64 {
    Some(n as u32)
```

`0.5` therefore returns `Some(0)` — a position the tool never reported, in a scheme where lines are
1-based.

That is the exact failure the module exists to prevent. Its own doc comment names it:

> a bare `n as u32` / `n as i64` cast is lossy at the edges: Rust _saturates_ out-of-range values
> and silently truncates the fractional part (`2.5 as i64` → `2`). Both turn malformed input into a
> plausible-but-wrong value instead of "absent". These helpers reject those cases up front.

Its sibling `json_int` does check `fract()`; `json_u32` does not. Whatever the reason, the two now
disagree, and only one of them matches the paragraph above.

## Why it has not mattered

No tool in the catalogue emits a fractional line or column, so the branch is unreachable in
practice. It was found by feeding a parser deliberately malformed input while writing its tests, not
by a tool misbehaving.

The cost is bounded but real: the one case the helper was written to catch is the one it lets
through, and a reader of the doc comment will believe otherwise.

## Shape of a fix

Add `n.fract() == 0.0` to the guard, matching `json_int`.

One thing to be careful of: `json_u32(0.0)` returning `Some(0)` is deliberate, asserted by
`json_u32_accepts_in_range_integers`, and relied on — pylint and npm-groovy-lint report 0-based
columns, which arrive as a legitimate `0`. So the guard is about the fractional part only; a
`n >= 1.0` floor would break those parsers.

The change is confined to input that is already malformed: today such a value becomes a truncated
integer, afterwards it becomes absent, which is what every other rejected shape already does. Around
25 parsers route positions through this helper, so the blast radius is wide but the behaviour change
is not reachable from any well-formed tool output.
