---
worth: yes
where: cmd/config_loader.go:432
added: 2026-09-23
---

# Managed config layering skips every layer loaded through `getRemoteConfigs()`

Managed config content is evaluated per layer, threading `existingContent` from one layer's output
to the next — but only for the top-level sources of the chain (`loadConfigImpl`, around
`cmd/config_loader.go:426` and `:432`). A layer resolved through `getRemoteConfigs()` is processed
inside `processConfigSource` (`:787`) and never becomes a layer of its own: its entries reach the
next top-level layer as input, and are evaluated there, in that layer's context.

That is harmless while the parent passes the remote entry through with `{...input}`: the Go value
keeps its content function, and the parent's evaluation runs it once. It goes wrong as soon as the
parent layers on top. A remote layer renders `"base"`; the parent replaces `content` with one that
appends to `existingContent`; the result is `undefined` plus the parent's part, because nothing
evaluated the remote layer first. The same gap moves what `expectChainHash` pins: the documented
contract is the output of the whole upstream chain (remote and before layers), but a remote layer's
output is never recorded as the content entering the root layer.

The eager pass (reconciliation, `config chain-hash`), the internal-placement render
(`RenderManagedConfigFromScratch`) and the pristine render reconciliation compares deletions against
all share the one layer list, so they agree with each other — which is why fixing the replay alone
would be wrong: a replay that saw remote layers and an eager pass that did not would disagree, and
every such file would read as customized.

The fix is to record each remote layer — its managed configs and the VM that owns their functions —
in chain order, and feed that one list to the eager pass and to the replay alike. Its cost is the
point to weigh: every existing `expectChainHash` pin taken over a remote chain was computed on the
current behaviour and would report drift once.

Found by an external review of the ejectable managed config change (docs/plans/completed/2026-09-23-managed-config-eject.md).
