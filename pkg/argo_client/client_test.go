package argo_client

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/zapier/kubechecks/pkg/config"
)

func TestArgoDBIsShared(t *testing.T) {
	k8s := fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-cm",
			Namespace: "argocd",
			Labels:    map[string]string{"app.kubernetes.io/part-of": "argocd"},
		},
	})
	a := &ArgoClient{k8s: k8s, cfg: config.ServerConfig{ArgoCDNamespace: "argocd"}}
	ctx := context.Background()

	_, err := a.ArgoDB().ListHelmRepositories(ctx)
	require.NoError(t, err)
	baseline := runtime.NumGoroutine()

	for range 50 {
		settingsMgr, argoDB := a.argoSettings()
		_, err := argoDB.ListHelmRepositories(ctx)
		require.NoError(t, err)
		_, err = settingsMgr.GetHelmSettings()
		require.NoError(t, err)
	}

	assert.Same(t, a.ArgoDB(), a.ArgoDB())
	assert.LessOrEqual(t, runtime.NumGoroutine(), baseline+5)
}
