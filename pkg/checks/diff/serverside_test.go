package diff

import (
	"testing"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/utils/kube"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func rd(name, live string) *argoappv1.ResourceDiff {
	return &argoappv1.ResourceDiff{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: name, LiveState: live}
}

// Batching must keep every resource in exactly one batch and never exceed the
// size cap unless a single resource is larger than the cap on its own.
func TestBatchBySize(t *testing.T) {
	live := []*argoappv1.ResourceDiff{rd("a", "12345"), rd("b", "12345"), rd("c", "12345")}
	targets := []string{"12345", "12345", "12345"}

	// 10 bytes per resource, cap of 20 => two per batch.
	batches := batchBySize(live, targets, 20)
	assert.Equal(t, []sizeBatch{{0, 2}, {2, 3}}, batches)

	// Every resource is covered exactly once.
	covered := 0
	for _, b := range batches {
		covered += b.end - b.start
	}
	assert.Equal(t, len(live), covered)

	// A cap smaller than one resource still makes progress rather than looping.
	single := batchBySize(live, targets, 1)
	assert.Len(t, single, 3)
}

func TestJSONOrNil(t *testing.T) {
	assert.Nil(t, jsonOrNil(""), "empty state must not become invalid JSON")
	assert.Nil(t, jsonOrNil("null"), `"null" must not become invalid JSON`)
	assert.Equal(t, []byte(`{"a":1}`), jsonOrNil(`{"a":1}`))
}

func TestFindResourceDiff(t *testing.T) {
	resources := []*argoappv1.ResourceDiff{rd("a", "x"), rd("b", "y")}
	key := kube.ResourceKey{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "b"}
	found := findResourceDiff(resources, key)
	if assert.NotNil(t, found) {
		assert.Equal(t, "y", found.LiveState)
	}
	missing := kube.ResourceKey{Group: "apps", Kind: "Deployment", Namespace: "ns", Name: "nope"}
	assert.Nil(t, findResourceDiff(resources, missing))
}
