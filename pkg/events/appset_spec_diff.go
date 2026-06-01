package events

import (
	"context"
	"fmt"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/rs/zerolog"
	"sigs.k8s.io/yaml"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
)

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
			out = append(out, appSetSpecFinding{
				AppName:    name,
				AppSetName: appSetName,
				Action:     "added by AppSet",
				Body:       renderAppYAML(&head),
				State:      pkg.StateSuccess,
			})
			continue
		}
		diff, err := unifiedAppDiff(&base, &head)
		if err != nil {
			logger.Warn().Err(err).Str("app", name).Msg("failed to compute appset spec diff")
			continue
		}
		if diff == "" {
			continue
		}
		out = append(out, appSetSpecFinding{
			AppName:    name,
			AppSetName: appSetName,
			Action:     "modified by AppSet",
			Body:       diff,
			State:      pkg.StateSuccess,
		})
	}

	for name, base := range baseByName {
		if _, stillThere := headByName[name]; stillThere {
			continue
		}
		out = append(out, appSetSpecFinding{
			AppName:    name,
			AppSetName: appSetName,
			Action:     "removed by AppSet",
			Body:       renderAppYAML(&base),
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

// comparableApp is the subset of an Application that's meaningful to compare
// across AppSet generations. Everything else (status, managed fields, etc.)
// is reconciler state and would only add noise.
type comparableApp struct {
	Name        string                          `json:"name"`
	Namespace   string                          `json:"namespace,omitempty"`
	Annotations map[string]string               `json:"annotations,omitempty"`
	Labels      map[string]string               `json:"labels,omitempty"`
	Spec        v1alpha1.ApplicationSpec        `json:"spec"`
	Operation   *v1alpha1.Operation             `json:"operation,omitempty"`
}

func toComparable(app *v1alpha1.Application) comparableApp {
	return comparableApp{
		Name:        app.Name,
		Namespace:   app.Namespace,
		Annotations: app.Annotations,
		Labels:      app.Labels,
		Spec:        app.Spec,
	}
}

func renderAppYAML(app *v1alpha1.Application) string {
	c := toComparable(app)
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintf("# failed to marshal Application %s: %v", app.Name, err)
	}
	return fmt.Sprintf("```yaml\n%s```", string(b))
}

func unifiedAppDiff(base, head *v1alpha1.Application) (string, error) {
	a, err := yaml.Marshal(toComparable(base))
	if err != nil {
		return "", err
	}
	b, err := yaml.Marshal(toComparable(head))
	if err != nil {
		return "", err
	}
	if string(a) == string(b) {
		return "", nil
	}
	var buf strings.Builder
	err = difflib.WriteUnifiedDiff(&buf, difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(a)),
		B:        difflib.SplitLines(string(b)),
		FromFile: "base",
		ToFile:   "head",
		Context:  2,
	})
	if err != nil {
		return "", err
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
	})
}
