package argo_client

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
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
	assert.Less(t, runtime.NumGoroutine(), baseline+50, "a SettingsManager per call leaks ~28 goroutines each")
}

func TestNewClientWithRetry(t *testing.T) {
	orig := newClientBackOff
	t.Cleanup(func() { newClientBackOff = orig })
	newClientBackOff = func() backoff.BackOff { return backoff.NewConstantBackOff(time.Millisecond) }

	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection timed out")}

	t.Run("retries transient dial errors", func(t *testing.T) {
		calls := 0
		closer, got, err := newClientWithRetry(context.Background(), "Test Client", func() (io.Closer, string, error) {
			calls++
			if calls < 3 {
				return nil, "", dialErr
			}
			return io.NopCloser(nil), "client", nil
		})
		require.NoError(t, err)
		assert.Equal(t, 3, calls)
		assert.Equal(t, "client", got)
		assert.NotNil(t, closer)
	})

	t.Run("does not retry permanent errors", func(t *testing.T) {
		calls := 0
		_, _, err := newClientWithRetry(context.Background(), "Test Client", func() (io.Closer, string, error) {
			calls++
			return nil, "", errors.New("invalid server address")
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("stops when the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, _, err := newClientWithRetry(ctx, "Test Client", func() (io.Closer, string, error) {
			calls++
			cancel()
			return nil, "", dialErr
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})
}
