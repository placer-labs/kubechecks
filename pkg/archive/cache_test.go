package archive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheRefCountWithConcurrentCallers(t *testing.T) {
	zipBytes := buildTestZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	c := NewCache(Config{BaseDir: t.TempDir(), TTL: time.Nanosecond})
	defer c.Shutdown()

	const sha = "deadbeef"
	const callers = 5
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetOrDownload(context.Background(), srv.URL+"/archive.zip", sha, nil)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, int32(callers), c.entries[sha].refCount)

	for range callers - 1 {
		c.Release(srv.URL, sha)
	}
	c.cleanupStaleArchives()
	require.Contains(t, c.entries, sha, "archive with a live reference must survive cleanup")

	c.Release(srv.URL, sha)
	c.Release(srv.URL, sha)
	assert.Equal(t, int32(0), c.entries[sha].refCount)

	c.cleanupStaleArchives()
	assert.NotContains(t, c.entries, sha)
}
