package core

import "testing"

func instances(group string) ResourceType {
	return ResourceType{
		Group: group, Version: "v1", Kind: "Instance",
		Plural: "instances", Singular: "instance", ShortNames: []string{"inst"},
	}
}

// The bare forms are what somebody types when nothing is ambiguous.
func TestMatchesNameAcceptsEveryBareForm(t *testing.T) {
	rt := instances("ec2.aws.whoctl.io")
	for _, name := range []string{"instances", "instance", "Instance", "INSTANCES", "inst"} {
		if !rt.MatchesName(name) {
			t.Errorf("%q does not name the kind", name)
		}
	}
	for _, name := range []string{"instanc", "instancesx", ""} {
		if rt.MatchesName(name) {
			t.Errorf("%q names the kind and should not", name)
		}
	}
}

// The qualified form is what tells two kinds sharing a plural apart, and it is
// kubectl's syntax: `kubectl get jobs.batch`.
func TestMatchesNameAcceptsTheQualifiedForm(t *testing.T) {
	ec2 := instances("ec2.aws.whoctl.io")
	rds := instances("rds.aws.whoctl.io")

	for _, name := range []string{"instances.ec2", "instances.ec2.aws", "instances.ec2.aws.whoctl.io", "inst.ec2"} {
		if !ec2.MatchesName(name) {
			t.Errorf("%q does not name the ec2 kind", name)
		}
		if rds.MatchesName(name) {
			t.Errorf("%q names the rds kind, so the qualifier did nothing", name)
		}
	}
}

// A group is cut at a label and nowhere else. Matching any prefix would make
// "ec" name "ec2.aws.whoctl.io", and "aws" name every group under it at once —
// which is the difference between a qualifier and a guess.
func TestAGroupIsCutAtALabel(t *testing.T) {
	rt := instances("ec2.aws.whoctl.io")
	for _, prefix := range []string{"ec2", "ec2.aws", "ec2.aws.whoctl.io"} {
		if !rt.MatchesGroupPrefix(prefix) {
			t.Errorf("%q does not name the group", prefix)
		}
	}
	for _, prefix := range []string{"ec", "e", "aws", "aws.whoctl.io", "whoctl.io", "", "ec2.aws.whoctl.io.x"} {
		if rt.MatchesGroupPrefix(prefix) {
			t.Errorf("%q names the group and should not", prefix)
		}
	}
}

func TestCollectionKindDefaultsToTheConvention(t *testing.T) {
	if got := instances("ec2.aws.whoctl.io").CollectionKind(); got != "InstanceList" {
		t.Errorf("listKind = %q", got)
	}
	rt := instances("ec2.aws.whoctl.io")
	rt.ListKind = "InstanceCollection"
	if got := rt.CollectionKind(); got != "InstanceCollection" {
		t.Errorf("a declared listKind was ignored: %q", got)
	}
}

func TestGVKReadsLikeKubernetesSpellsIt(t *testing.T) {
	if got := instances("ec2.aws.whoctl.io").GVK().String(); got != "ec2.aws.whoctl.io/v1, Kind=Instance" {
		t.Errorf("gvk = %q", got)
	}
}
