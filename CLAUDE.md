# whoctl-sdk-go

What a provider author imports. Every package here is public API for somebody
whose repository you cannot see.

`core` is the object model and the `Handler` interface; `protocol` is the wire
contract and both halves of it; `schema` is the `doc` tag vocabulary and the
reflection that reads it; `sysexec` is the command choke point; `docs` builds a
provider's documentation and its bundle; `providertest` is the conformance suite
a provider runs against itself.

## Changing this is changing a contract

**Two sides implement the protocol and they are released separately.** A
provider built against an older SDK will be talking to a newer whoctl and the
other way round. Bump `protocol.Version` only when the old side would
*misbehave* — never for something added, which is what the version exists to
avoid having to do.

**The bundle format is the same kind of promise.** `docs.BundleFormat` guards
the shape, and the json tags on the model *are* the format: renaming a Go field
changes what every provider emits, silently. That is why they are spelled out
rather than inherited from field names.

**Nothing here may assume a provider is written in Go.** The protocol is
newline-delimited JSON over stdio precisely so a provider can be a Python
script. This module exists to make the Go case pleasant, not to make it
mandatory.

## Decisions somebody would otherwise undo

**One schema serves documentation and validation.** The CLI has no access to a
provider's Go types across a process boundary, so the provider publishes the
field records — the same ones the tables are generated from. Two descriptions of
the same field would eventually disagree.

**`core.Error` carries a code, not a type.** `errors.As` on a Go type is what a
single process would do and it does not survive the trip. The set is small on
purpose: add one only when it changes what a *user* does next.

**`Cause` does not travel.** It is documented as such on `core.Error`, and
nothing downstream may come to depend on it: `Message` is all the far side
sends.

**A provider says where its pages live.** `ProviderDocs.PagePath` exists because
deriving the path from a kind's singular imposes a layout on every provider, and
one that groups its kinds into families cannot follow it.

**`providertest` is here, not in whoctl.** whoctl cannot test a provider it has
never seen, and somebody else's provider needs the same answers about columns,
capabilities and documentation as ours do.

**`sysexec` is what makes `--dry-run` mean something** for a provider whoctl did
not write. The flag is enforced by the package the provider imports rather than
trusted. That is not a guarantee — see the design note on what the split costs —
but it is the difference between honouring it by default and remembering to.

## Safety

Nothing in this module touches a machine. Its tests are pure, and they are the
only tests in the workspace that need no fixture tree at all. The rules about
mutation live with the providers that mutate.
