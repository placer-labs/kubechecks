package github_client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/go-github/v93/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg/config"
)

var (
	recorderOnce sync.Once
	recorder     *tracetest.SpanRecorder
)

func spanRecorder() *tracetest.SpanRecorder {
	recorderOnce.Do(func() {
		recorder = tracetest.NewSpanRecorder()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	})
	return recorder
}

func spansInTrace(rec *tracetest.SpanRecorder, traceID trace.TraceID) map[string]sdktrace.ReadOnlySpan {
	spans := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		if s.SpanContext().TraceID() == traceID {
			spans[s.Name()] = s
		}
	}
	return spans
}

func TestCreateHttpClient_TokenRequestsAreTraced(t *testing.T) {
	rec := spanRecorder()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer a-token", r.Header.Get("Authorization"))
	}))
	t.Cleanup(srv.Close)

	httpClient, _, err := createHttpClient(config.ServerConfig{VcsToken: "a-token"})
	require.NoError(t, err)

	ctx, root := otel.Tracer("test").Start(context.Background(), "root")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	root.End()

	spans := spansInTrace(rec, root.SpanContext().TraceID())
	get, ok := spans["HTTP GET"]
	require.True(t, ok, "no client span in %v", spans)
	assert.Equal(t, trace.SpanKindClient, get.SpanKind())
	assert.Equal(t, root.SpanContext().SpanID(), get.Parent().SpanID())
}

func TestTidyOutdatedComments_SpansNest(t *testing.T) {
	rec := spanRecorder()
	issues := &fakeCommentList{pages: [][]*github.IssueComment{{botComment(1, "kubechecks-bot")}}}
	gql := &graphqlRecorder{minimized: map[string]bool{"IC_1": true}}
	c := &Client{
		googleClient:   &GClient{Issues: issues},
		shurcoolClient: gql.serve(t),
		username:       "kubechecks-bot",
	}

	ctx, root := otel.Tracer("test").Start(context.Background(), "root")
	require.NoError(t, c.TidyOutdatedComments(ctx, testPR))
	root.End()

	spans := spansInTrace(rec, root.SpanContext().TraceID())
	require.Contains(t, spans, "TidyOutdatedComments")
	require.Contains(t, spans, "hideOutdatedComments")
	assert.Equal(t, root.SpanContext().SpanID(), spans["TidyOutdatedComments"].Parent().SpanID())
	assert.Equal(t, spans["TidyOutdatedComments"].SpanContext().SpanID(), spans["hideOutdatedComments"].Parent().SpanID())
}
