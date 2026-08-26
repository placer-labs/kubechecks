package events

import (
	"context"
	"fmt"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/appdir"
	checksdiff "github.com/zapier/kubechecks/pkg/checks/diff"
	"github.com/zapier/kubechecks/pkg/msg"
)

// filterAppsByChangeList narrows a slice of AppSet-generated Applications
// to those whose source paths (and helm valueFiles / fileParameters) actually
// overlap with the PR's changed files. Reuses appdir.AppDirectory so the
// prefix/file matching is identical to what kubechecks does for plain
// Applications. Without this, a PR touching the AppSet's own config (the
// AppSet manifest's values.yaml) would queue every Application the AppSet
// generates — often hundreds — because the matcher flagged the AppSet as
// affected. Structural AppSet changes still surface via the spec-diff
// findings emitted alongside.
func filterAppsByChangeList(apps []v1alpha1.Application, changeList []string, targetBranch string, logger zerolog.Logger) []v1alpha1.Application {
	dir := appdir.NewAppDirectory()
	for _, app := range apps {
		dir.AddApp(app)
	}
	matched := dir.FindAppsBasedOnChangeList(changeList, targetBranch)
	logger.Info().
		Int("generated", len(apps)).
		Int("queued", len(matched)).
		Msg("filtered AppSet-generated apps by changed files")
	return matched
}

// appSetSpecFinding describes a per-Application change introduced by an
// AppSet between the PR base and head. Collected during AppSet processing
// and flushed into vcsNote once that note exists.
type appSetSpecFinding struct {
	AppName    string
	AppSetName string
	Action     string // "added by AppSet", "removed by AppSet", "modified by AppSet"
	Body       string // rendered markdown details (code-fenced YAML or diff)
	State      pkg.CommitState
}

// computeAppSetSpecDiff compares the Applications an AppSet would generate at
// the PR HEAD against the Applications it would generate at the AppSet's
// configured revision (base) and returns one finding per affected name:
//
//   - "added by AppSet": the AppSet starts generating this Application from
//     this PR — typically because the PR adds a values/variants file the Git
//     generator's glob matches.
//   - "removed by AppSet": the AppSet stops generating this Application —
//     typically because the PR removes a values file.
//   - "modified by AppSet": the AppSet still generates this Application but
//     its spec differs (e.g. source.path or tracking-id changed).
//
// Without this, AppSet "handover" PRs (where a parent app-of-apps stops
// templating an Application and an AppSet starts generating it with the
// same name at a new source path) show only the parent's "removed" diff
// and the helm-render check reports "no changes" — the AppSet's addition
// is invisible.
func computeAppSetSpecDiff(
	logger zerolog.Logger,
	appSetName string,
	headApps, baseApps []v1alpha1.Application,
) []appSetSpecFinding {
	headByName := indexApps(headApps)
	baseByName := indexApps(baseApps)

	var out []appSetSpecFinding

	for name, head := range headByName {
		base, hadBefore := baseByName[name]
		if !hadBefore {
			body, err := unifiedAppDiff(nil, &head)
			if err != nil {
				logger.Warn().Err(err).Str("app", name).Msg("failed to render added-by-appset diff")
				continue
			}
			out = append(out, appSetSpecFinding{
				AppName:    name,
				AppSetName: appSetName,
				Action:     "added by AppSet",
				Body:       body,
				State:      pkg.StateSuccess,
			})
			continue
		}
		body, err := unifiedAppDiff(&base, &head)
		if err != nil {
			logger.Warn().Err(err).Str("app", name).Msg("failed to compute appset spec diff")
			continue
		}
		if body == "" {
			continue
		}
		out = append(out, appSetSpecFinding{
			AppName:    name,
			AppSetName: appSetName,
			Action:     "modified by AppSet",
			Body:       body,
			State:      pkg.StateSuccess,
		})
	}

	for name, base := range baseByName {
		if _, stillThere := headByName[name]; stillThere {
			continue
		}
		body, err := unifiedAppDiff(&base, nil)
		if err != nil {
			logger.Warn().Err(err).Str("app", name).Msg("failed to render removed-by-appset diff")
			continue
		}
		out = append(out, appSetSpecFinding{
			AppName:    name,
			AppSetName: appSetName,
			Action:     "removed by AppSet",
			Body:       body,
			State:      pkg.StateSuccess,
		})
	}
	return out
}

// flushAppSetSpecFindings dispatches collected findings into vcsNote. Safe to
// call only after vcsNote is non-nil.
func flushAppSetSpecFindings(ctx context.Context, vcsNote *msg.Message, findings []appSetSpecFinding) {
	if vcsNote == nil {
		return
	}
	for _, f := range findings {
		attachAppSetResult(ctx, vcsNote, f.AppName, f.AppSetName, f.Action, f.Body, f.State)
	}
}

func indexApps(apps []v1alpha1.Application) map[string]v1alpha1.Application {
	out := make(map[string]v1alpha1.Application, len(apps))
	for _, a := range apps {
		out[a.Name] = a
	}
	return out
}

// appToUnstructured converts a freshly-generated AppSet Application to the
// Unstructured form that pkg/checks/diff.PrintDiff consumes. Returns nil for
// a nil input so PrintDiff renders pure additions/removals correctly. Strips
// reconciler-only state that would only ever be noise when comparing two
// generator outputs (neither side has been reconciled yet, but be defensive).
func appToUnstructured(app *v1alpha1.Application) (*unstructured.Unstructured, error) {
	if app == nil {
		return nil, nil
	}
	clean := app.DeepCopy()
	clean.Status = v1alpha1.ApplicationStatus{}
	clean.ManagedFields = nil
	clean.ResourceVersion = ""
	clean.UID = ""
	clean.Generation = 0
	clean.CreationTimestamp.Time = clean.CreationTimestamp.Truncate(0)

	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(clean)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: raw}, nil
}

// unifiedAppDiff produces a markdown ```diff``` block comparing two
// Applications. Passing nil for base renders an "addition" (all lines
// prefixed with `+`); passing nil for head renders a "removal" (all lines
// prefixed with `-`). Reuses pkg/checks/diff.PrintDiff so the rendering
// matches kubechecks's existing resource diffs.
func unifiedAppDiff(base, head *v1alpha1.Application) (string, error) {
	live, err := appToUnstructured(base)
	if err != nil {
		return "", err
	}
	target, err := appToUnstructured(head)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := checksdiff.PrintDiff(&buf, live, target); err != nil {
		return "", err
	}
	if buf.Len() == 0 {
		return "", nil
	}
	return fmt.Sprintf("```diff\n%s```", buf.String()), nil
}

func attachAppSetResult(
	ctx context.Context,
	vcsNote *msg.Message,
	appName, appSetName, action, body string,
	state pkg.CommitState,
) {
	vcsNote.AddNewApp(ctx, appName)
	vcsNote.AddToAppMessage(ctx, appName, msg.Result{
		State:   state,
		Summary: fmt.Sprintf("%s `%s`", action, appSetName),
		Details: body,
		// Bypass the "any NoChangesDetected sibling suppresses the whole
		// app section" filter — AppSet structural changes are real news
		// even when the workload diff happens to be identical.
		Sticky: true,
	})
}
