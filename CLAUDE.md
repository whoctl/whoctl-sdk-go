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

## The object model is Kubernetes', on purpose

**A kind is identified by group, version and kind — never by kind alone.** A
kind is unique inside its group and nowhere else, which is what lets one
provider serve `Instance` under `ec2.` and another `Instance` under `rds.`, the
way ACK and Crossplane both do it. Dispatch keyed on the name alone did not fail
loudly there: the server kept handlers in a `map[string]`, and one silently
answered for the other. `providertest.Naming` is what catches it now.

**The group is per kind, not per provider.** A provider's helper that stamps one
group on everything is fine for a provider covering one thing and wrong for one
covering a cloud. The group is also where the command line's middle segment
comes from: `aws/route53/hostedzones` is `route53.aws.whoctl.io` read backwards,
and `MatchesGroupPrefix` is what cuts it at a label. Nothing about that reaches
the wire — what travels is the triple.

**Verbs and capabilities are two vocabularies for two audiences.**
`ResourceType.Verbs` is Kubernetes' closed set and goes into what is, in all but
name, a discovery document. `Capability` is what whoctl's own commands ask
about, and half of it — `restart`, `describe` — means nothing to a Kubernetes
client, where a restart is a patch on an annotation. Folding them together would
publish words no client can act on.

**`metadata` carries the machinery, not just the name.** `uid`,
`resourceVersion` and `creationTimestamp` are what make a client more than a
table: AGE is drawn from the timestamp for every kind, and a watch resumes from
a version. `core.Time` exists because Go writes nanoseconds and a zero time as
year 1, and Kubernetes writes seconds and `null` — compatible in Go, not on the
wire, and only a real client would have found it.

**A namespace is scope, and scope crosses the wire.** It travels on the context
the way delete options do, and for the same reason it has an explicit field on
every params: a scope that only rode a context arrives as its zero value, and
the failure is not an error — it is an answer about the wrong slice of the
world. `TestEveryScopeFieldCrossesTheWire` fails when a field is added on one
side only.

**Scope is three answers, not two.** A named namespace, every namespace, and
*whichever one is the default* — and only the provider can answer the third: the
aws provider's default region comes from the same AWS configuration it
authenticates with, and nothing above it can read that. So `AllNamespaces` is a
field of its own rather than an empty `Namespace` doing double duty. Kubernetes
draws the same line in the URL, between `/apis/g/v/resource` and
`/apis/g/v/namespaces/ns/resource`; this is that choice, spelled as a field.

**Watch is the one method that streams, and the one that brings concurrency.**
Its frames share a request id and are marked `Stream` until the last, which is
why `Subprocess` now demultiplexes instead of reading the next line back — with
a watch open, that next line is somebody else's event. On the provider side a
watch runs in its own goroutine so the requests behind it are still served, and
that concurrency reaches exactly the handlers implementing `core.Watcher`:
implementing it is how a provider says its `Watch` may run beside its other
verbs.

## A provider never learns who started it

The same binary serves two situations and must not be able to tell them apart:
somebody running whoctl on their own machine, with whatever permissions they
happen to have, and a whoctl server that has several of them configured — ten
AWS accounts, say — and hands them out to whoever connects.

**The only thing that differs is who writes the process's environment.** In the
first case it is the user's own shell and whatever they already have: `~/.aws`,
their SSO session, their uid. In the second it is the server, per context. There
is no flag, no handshake field and no branch in a provider for it.

What guarantees this is `Config` being tiny and not extensible per provider.
That was written down to stop the CLI carrying flags for kinds it knows nothing
about, and it is the same rule for the same reason: the moment a credential, an
endpoint or a context lands in `Config`, a provider has configuration only a
server can supply and running it yourself becomes the special case.

Three things follow, and they are what gets lost first:

- **Ambient credentials are the normal path, not a fallback.** A cloud provider
  follows its own vendor's credential chain as its default behaviour; a server
  does not use a different door, it just builds the environment for that
  context.
- **Attempt and report; never check permissions first.** This is what makes "it
  gets whatever I am allowed" true. Reads degrade rather than refuse: a user who
  cannot read `/etc/shadow` still gets `linux/users` from `/etc/passwd`, with
  the shadow fields absent. A read a credential cannot reach is a missing field
  or an error naming the permission — never a whole kind falling over.
- **Several copies may run at once on one machine**, with different credentials,
  possibly beside the user's own. So: no lock, socket, cache or temporary file
  at a fixed path. A singleton that is really a property of the *resource* — one
  Steam installation per machine — is a different thing and stays.

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
