# whoctl-sdk-go

What you import to write a [whoctl](https://github.com/whoctl/whoctl) provider
in Go.

A provider is a program whoctl runs and talks to over newline-delimited JSON on
stdio. This module is the other half of that conversation: you implement one
interface per kind, and it becomes a process whoctl can install and drive.

## Writing a provider

**1. A kind is a spec, a status and a handler.** The spec is what a manifest
declares; the status is what the machine actually says. Every field carries the
sentence that documents it:

```go
type WidgetSpec struct {
	Size  int    `yaml:"size" json:"size" doc:"How big to make it." docExample:"3"`
	Label string `yaml:"label,omitempty" json:"label,omitempty" doc:"Shown in the UI."`
}

type WidgetStatus struct {
	Size  int    `yaml:"size" json:"size" doc:"How big it is."`
	Label string `yaml:"label,omitempty" json:"label,omitempty" doc:"The label it carries."`
}
```

**2. Implement `core.Handler`.** Five verbs, and a `Type()` that names the kind
and describes its table:

```go
func (h *Handler) Type() core.ResourceType {
	return core.ResourceType{
		Group: "example.whoctl.io", Version: "v1alpha1",
		Kind: "Widget", Plural: "widgets", Singular: "widget",
		Description: "A widget.",
		Columns: []core.Column{
			{Name: "NAME", Path: "metadata.name"},
			{Name: "SIZE", Path: "status.size"},
		},
	}
}

func (h *Handler) NewSpec() any   { return &WidgetSpec{} }
func (h *Handler) NewStatus() any { return &WidgetStatus{} }

func (h *Handler) List(ctx context.Context) ([]core.Object, error)
func (h *Handler) Get(ctx context.Context, name string) (core.Object, error)
func (h *Handler) Apply(ctx context.Context, obj core.Object) (core.Result, error)
func (h *Handler) Delete(ctx context.Context, name string) error
```

`Apply` is an upsert and must report `unchanged` when nothing differs — that is
what makes `whoctl get -o yaml | whoctl apply -f -` a no-op rather than a
rewrite. `Get` and `Delete` return `core.NotFound(...)` for what is not there;
anything else a command distinguishes gets a code too — `core.Unsupportedf` for
a verb that will never exist, `core.Unavailablef` for tooling that is not on the
machine, `core.Refusedf` for something that would work later.

**3. Mutate only through `sysexec.Runner`**, which is what makes `--dry-run` and
`-v` mean anything.

**4. Serve it.** That is the whole `main`:

```go
func main() {
	protocol.ServeProcess(func(cfg protocol.Config) (core.Provider, error) {
		runner := &sysexec.Runner{DryRun: cfg.DryRun, Verbose: cfg.Verbose, Out: os.Stderr}
		return example.New(example.Options{Root: cfg.Root, Runner: runner}), nil
	}, version)
}
```

The provider is built *from* the configuration rather than reconfigured after
it: the root decides what it reads and the runner decides whether a mutation
runs, so a provider built before the handshake would have to be adjusted
afterwards, and "adjust" is where a flag gets forgotten.

**5. Run the conformance suite**, which is your whole contract with whoctl in
one test:

```go
func TestConformance(t *testing.T) {
	providertest.Conformance(t, example.New(example.Options{}), providertest.Options{SourceRoot: "."})
}
```

It fails on a column that names no field, a capability published but not
implemented, a field with no `doc` tag, a kind with no page, or a page whose
generated tables are stale.

**6. Publish.** A release ships the binary per platform and a documentation
bundle — `yourprovider --docs-bundle` — which is what puts your pages on the
site.

## Things that will bite

**stdout belongs to the protocol.** Anything you want a human to read goes to
stderr, which whoctl passes through untouched. That is why `-v` works at all.

**Nothing crosses on a `context`.** A context value is correct in one process
and silently nothing across two. Whatever a verb needs must be on the wire.

**Columns are data.** `Path` is spelled the way a manifest spells the field, and
`Format` comes from a closed vocabulary the CLI owns. You cannot ship a
formatter; the printer does not run in your process.

**Optional verbs are interfaces you may implement**, and whoctl reaches them
through the capabilities you publish: `core.Describer`, `core.Restarter`,
`core.ScopedLister` for a kind whose objects are not addressable alone.

## Layout

| Path | Role |
| --- | --- |
| `core` | The object model, `Handler`, the error codes, the column paths. |
| `protocol` | The wire contract, the stdio server, and a client for testing your own provider. |
| `schema` | The `doc` tag vocabulary and the reflection that reads it. |
| `sysexec` | The choke point for running external commands. |
| `docs` | Builds a provider's documentation and its release bundle. |
| `providertest` | The conformance suite. |

## One schema, two jobs

The `doc` tags are not comments. Across a process boundary whoctl has none of
your Go types, so what it gets is the schema you publish — and that same schema
generates your documentation tables. Two descriptions of one field would
eventually disagree; there is only one.

## A provider does not have to be written in Go

The protocol is newline-delimited JSON over stdin and stdout, so a provider can
be a Python script. This module exists to make the Go case pleasant, not to make
it mandatory. The wire contract is `protocol/protocol.go`.
