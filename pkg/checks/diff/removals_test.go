package diff

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/diff"
	argoappv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const lokiLive = `{
  "apiVersion": "apps/v1", "kind": "StatefulSet",
  "metadata": {"name": "loki-index-gateway", "namespace": "loki",
    "labels": {"app.kubernetes.io/name": "loki", "helm.sh/chart": "loki-20.0.0"}},
  "spec": {
    "selector": {"matchLabels": {"app.kubernetes.io/name": "loki"}},
    "template": {
      "metadata": {"labels": {"app.kubernetes.io/name": "loki", "app.kubernetes.io/part-of": "memberlist"}},
      "spec": {
        "affinity": {"podAntiAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": [{"topologyKey": "kubernetes.io/hostname"}]}},
        "containers": [{"name": "index-gateway", "image": "loki:3.7.9",
          "env": [{"name": "GOGC", "value": "80"}, {"name": "GOMEMLIMIT", "value": "3481MiB"}]}]}}}}`

const lokiTarget = `{
  "apiVersion": "apps/v1", "kind": "StatefulSet",
  "metadata": {"name": "loki-index-gateway", "namespace": "loki",
    "labels": {"app.kubernetes.io/name": "loki", "helm.sh/chart": "loki-20.2.0"}},
  "spec": {
    "selector": {"matchLabels": {"app.kubernetes.io/name": "loki"}},
    "template": {
      "metadata": {"labels": {"app.kubernetes.io/name": "loki"}},
      "spec": {
        "containers": [{"name": "index-gateway", "image": "loki:3.7.9",
          "env": [{"name": "GOGC", "value": "80"}]}]}}}}`

const lokiApplied = `{
  "f:metadata": {"f:labels": {"f:app.kubernetes.io/name": {}, "f:helm.sh/chart": {}}},
  "f:spec": {
    "f:selector": {},
    "f:template": {
      "f:metadata": {"f:labels": {"f:app.kubernetes.io/name": {}, "f:app.kubernetes.io/part-of": {}}},
      "f:spec": {
        "f:affinity": {"f:podAntiAffinity": {"f:requiredDuringSchedulingIgnoredDuringExecution": {}}},
        "f:containers": {"k:{\"name\":\"index-gateway\"}": {".": {}, "f:name": {}, "f:image": {},
          "f:env": {
            "k:{\"name\":\"GOGC\"}": {".": {}, "f:name": {}, "f:value": {}},
            "k:{\"name\":\"GOMEMLIMIT\"}": {".": {}, "f:name": {}, "f:value": {}}}}}}}}}`

func mustObj(t *testing.T, s string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	require.NoError(t, json.Unmarshal([]byte(s), &u.Object))
	return u
}

func entry(manager string, op metav1.ManagedFieldsOperationType, fields string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{
		Manager:    manager,
		Operation:  op,
		APIVersion: "apps/v1",
		FieldsType: "FieldsV1",
		FieldsV1:   &metav1.FieldsV1{Raw: []byte(fields)},
	}
}

func predictedLabels(t *testing.T, res diff.DiffResult) map[string]any {
	t.Helper()
	obj := mustObj(t, string(res.PredictedLive))
	labels, _, err := unstructured.NestedMap(obj.Object, "spec", "template", "metadata", "labels")
	require.NoError(t, err)
	return labels
}

func localDiff(t *testing.T) diff.DiffResult {
	t.Helper()
	res, err := diff.Diff(mustObj(t, lokiTarget), mustObj(t, lokiLive))
	require.NoError(t, err)
	return *res
}

// Without a last-applied annotation the local diff is a two-way merge that
// never deletes: the label dropped from the chart survives in PredictedLive.
func TestLocalDiffHidesRemovedField(t *testing.T) {
	res := localDiff(t)
	assert.Contains(t, predictedLabels(t, res), "app.kubernetes.io/part-of")
}

func TestApplyFieldRemovals_SoleApplyOwnerIsRemoved(t *testing.T) {
	res := localDiff(t)
	mf := []metav1.ManagedFieldsEntry{
		entry(argoCDSSAManager, metav1.ManagedFieldsOperationApply, lokiApplied),
		{Manager: "kube-controller-manager", Operation: metav1.ManagedFieldsOperationUpdate, Subresource: "status",
			FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:status":{"f:replicas":{}}}`)}},
	}

	retained, err := applyFieldRemovals(&res, mf, mustObj(t, lokiTarget))
	require.NoError(t, err)
	assert.Empty(t, retained)
	assert.True(t, res.Modified)

	assert.NotContains(t, predictedLabels(t, res), "app.kubernetes.io/part-of")

	predicted := mustObj(t, string(res.PredictedLive))
	_, found, _ := unstructured.NestedFieldNoCopy(predicted.Object, "spec", "template", "spec", "affinity")
	assert.False(t, found, "a removed block is dropped whole, not left as empty parents")

	containers, _, _ := unstructured.NestedSlice(predicted.Object, "spec", "template", "spec", "containers")
	require.Len(t, containers, 1)
	env := containers[0].(map[string]any)["env"].([]any)
	assert.Equal(t, []any{map[string]any{"name": "GOGC", "value": "80"}}, env)

	var b strings.Builder
	require.NoError(t, PrintDiff(&b, mustObj(t, lokiLive), predicted))
	assert.Contains(t, b.String(), "-        app.kubernetes.io/part-of: memberlist")
}

// A field another manager co-owns stays live after an SSA sync, so it must be
// reported rather than rendered as a deletion.
func TestApplyFieldRemovals_CoOwnedFieldIsRetained(t *testing.T) {
	res := localDiff(t)
	mf := []metav1.ManagedFieldsEntry{
		entry(argoCDSSAManager, metav1.ManagedFieldsOperationApply, lokiApplied),
		entry("helm", metav1.ManagedFieldsOperationUpdate,
			`{"f:spec":{"f:template":{"f:metadata":{"f:labels":{"f:app.kubernetes.io/part-of":{}}}}}}`),
	}

	retained, err := applyFieldRemovals(&res, mf, mustObj(t, lokiTarget))
	require.NoError(t, err)
	require.Len(t, retained, 1)
	assert.Equal(t, ".spec.template.metadata.labels.app.kubernetes.io/part-of", retained[0].path.String())
	assert.Equal(t, []string{"helm/Update"}, retained[0].owners)
	assert.Contains(t, predictedLabels(t, res), "app.kubernetes.io/part-of")

	var b strings.Builder
	writeRetained(&b, retained)
	assert.Equal(t, "! removed from git but kept on sync, still owned by helm/Update: .spec.template.metadata.labels.app.kubernetes.io/part-of\n", b.String())
}

// Ownership left by a past client-side sync: the field came from git, and SSA
// will never remove it.
func TestApplyFieldRemovals_ClientSideResidueIsRetained(t *testing.T) {
	res := localDiff(t)
	mf := []metav1.ManagedFieldsEntry{
		entry(argoCDSSAManager, metav1.ManagedFieldsOperationUpdate,
			`{"f:spec":{"f:template":{"f:metadata":{"f:labels":{"f:app.kubernetes.io/name":{},"f:app.kubernetes.io/part-of":{}}}}}}`),
	}

	retained, err := applyFieldRemovals(&res, mf, mustObj(t, lokiTarget))
	require.NoError(t, err)
	require.Len(t, retained, 1)
	assert.Equal(t, ".spec.template.metadata.labels.app.kubernetes.io/part-of", retained[0].path.String())
	assert.Equal(t, []string{"argocd-controller/Update"}, retained[0].owners)
	assert.Contains(t, predictedLabels(t, res), "app.kubernetes.io/part-of")
}

func TestApplyFieldRemovals_NoManagedFieldsIsNoop(t *testing.T) {
	res := localDiff(t)
	before := string(res.PredictedLive)

	retained, err := applyFieldRemovals(&res, nil, mustObj(t, lokiTarget))
	require.NoError(t, err)
	assert.Empty(t, retained)
	assert.Equal(t, before, string(res.PredictedLive))
}

func TestSyncsServerSide(t *testing.T) {
	target := mustObj(t, lokiTarget)
	assert.False(t, syncsServerSide(argoappv1.Application{}, target))

	app := argoappv1.Application{Spec: argoappv1.ApplicationSpec{SyncPolicy: &argoappv1.SyncPolicy{
		SyncOptions: argoappv1.SyncOptions{"CreateNamespace=true", "ServerSideApply=true"},
	}}}
	assert.True(t, syncsServerSide(app, target))

	target.SetAnnotations(map[string]string{"argocd.argoproj.io/sync-options": "ServerSideApply=true"})
	assert.True(t, syncsServerSide(argoappv1.Application{}, target))
}
