package docs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/whoctl/whoctl-sdk-go/core"
)

// A bundle carries everything a site needs and nothing about how it looks.
func TestBundleRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBundle(&buf, testProvider(), "1.2.3"); err != nil {
		t.Fatal(err)
	}

	b, err := ReadBundle(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != "1.2.3" {
		t.Errorf("version = %q", b.Version)
	}
	if b.Provider.Name == "" {
		t.Fatalf("provider = %+v", b.Provider)
	}
	if len(b.Provider.Resources) == 0 {
		t.Fatal("the bundle carries no resources")
	}

	// The fields the tables are generated from have to survive, or the site
	// renders headings with nothing under them.
	r := b.Provider.Resources[0]
	if len(r.Spec) == 0 || r.Spec[0].Doc == "" {
		t.Errorf("spec fields did not survive: %+v", r.Spec)
	}
	if r.Body == "" {
		t.Error("the page body did not survive")
	}
}

// A site built for one format must refuse another rather than guess.
func TestABundleFromAnotherFormatIsRefused(t *testing.T) {
	_, err := ReadBundle(strings.NewReader(`{"format":999,"provider":{"name":"x"}}`))
	if err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("err = %v, want a refusal naming the format", err)
	}
}

func TestABundleWithoutAProviderIsRefused(t *testing.T) {
	if _, err := ReadBundle(strings.NewReader(`{"format":1}`)); err == nil {
		t.Error("a bundle naming no provider must be refused")
	}
}

// The site decides which providers it covers and in what order; a bundle does
// not get an opinion.
func TestSiteOfKeepsTheOrderItWasGiven(t *testing.T) {
	a := &Bundle{Format: BundleFormat, Provider: Provider{Name: "b"}}
	b := &Bundle{Format: BundleFormat, Provider: Provider{Name: "a"}}
	site := SiteOf([]*Bundle{a, b}, Options{})
	if len(site.Providers) != 2 || site.Providers[0].Name != "b" {
		t.Errorf("providers = %+v", site.Providers)
	}
	if site.Title == "" {
		t.Error("a site with no title given must still have one")
	}
}

// testProvider is the same fixture the rest of this package builds sites from.
func testProvider() core.Provider {
	return &fakeProvider{files: testFiles(), spec: &sampleSpec{}}
}
