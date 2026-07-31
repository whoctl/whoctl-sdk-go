package docs

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// BundleFormat is the version of the bundle layout. It changes when a site
// built for an older format would misread a newer bundle, which is the only
// reason to change it: adding a field does not.
const BundleFormat = 1

// A Bundle is a provider's documentation, published as one file.
//
// # Why a bundle and not a site
//
// A provider knows its own pages and its own schema and nothing else. A site
// knows how things look and which providers it covers. Between them there has
// to be an artifact carrying content and no markup, and this is it: the
// provider emits one per release, the site fetches the ones it lists.
//
// That split is what keeps the templates in one place — change them and the
// whole site changes, with nobody re-releasing anything — and it is also a
// boundary worth having once somebody else's provider is on the site, because
// markdown and a schema cannot inject script into a page and rendered HTML can.
type Bundle struct {
	// Format is BundleFormat. A site that does not know the number refuses
	// rather than guessing at the shape.
	Format int `json:"format"`
	// Provider is the whole documentation model: the overview, the guides, and
	// every resource with its fields and columns.
	Provider Provider `json:"provider"`
	// Version is the provider release this came from, so a site can carry more
	// than one and a URL can be stable.
	Version string `json:"version,omitempty"`
}

// WriteBundle builds a provider's documentation and writes it as a bundle.
//
// It is what a provider's release runs. Everything in the result comes from the
// provider itself — the pages it embeds and the `doc` tags on its own structs —
// so a provider that documents itself completely produces a complete bundle,
// and one that does not fails its own conformance test long before this.
func WriteBundle(w io.Writer, p core.Provider, version string) error {
	site, err := Build([]core.Provider{p}, Options{})
	if err != nil {
		return err
	}
	if len(site.Providers) != 1 {
		return fmt.Errorf("expected one provider, built %d", len(site.Providers))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Bundle{Format: BundleFormat, Provider: site.Providers[0], Version: version})
}

// ReadBundle parses one.
func ReadBundle(r io.Reader) (*Bundle, error) {
	var b Bundle
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return nil, fmt.Errorf("reading a documentation bundle: %w", err)
	}
	if b.Format != BundleFormat {
		return nil, fmt.Errorf("documentation bundle is format %d, and this build reads %d", b.Format, BundleFormat)
	}
	if b.Provider.Name == "" {
		return nil, fmt.Errorf("documentation bundle names no provider")
	}
	return &b, nil
}

// SiteOf assembles a site from bundles, which is what the aggregator does.
//
// The order is the order they were given: which providers a site covers, and in
// what order, is the site's decision and not a bundle's.
func SiteOf(bundles []*Bundle, opts Options) *Site {
	site := &Site{Title: opts.Title, Version: opts.Version}
	if site.Title == "" {
		site.Title = "whoctl registry"
	}
	for _, b := range bundles {
		site.Providers = append(site.Providers, b.Provider)
	}
	return site
}
