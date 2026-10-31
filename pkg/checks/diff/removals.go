package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/argoproj/argo-cd/gitops-engine/pkg/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
	"sigs.k8s.io/structured-merge-diff/v6/value"
)

const argoCDSSAManager = "argocd-controller"

// retainedField is a field that git no longer renders but that a sync will not
// remove, because a field manager other than ArgoCD's apply still owns it.
type retainedField struct {
	path   fieldpath.Path
	owners []string
}

type ownedSet struct {
	owner string
	set   *fieldpath.Set
}

// applyFieldRemovals predicts what a server-side apply sync removes. The local
// diff has no last-applied annotation to work from on SSA-synced objects, so it
// falls back to a two-way diff that never deletes anything. Under SSA a field
// is removed when ArgoCD's apply manager owned it and the new manifest omits
// it, unless another manager also owns it. Removable fields are stripped from
// PredictedLive so they render as deletions; the rest are returned.
func applyFieldRemovals(diffRes *diff.DiffResult, managedFields []metav1.ManagedFieldsEntry, target *unstructured.Unstructured) ([]retainedField, error) {
	if target == nil || len(diffRes.PredictedLive) == 0 {
		return nil, nil
	}

	applied, others, err := splitManagedFields(managedFields)
	if err != nil {
		return nil, err
	}
	if applied == nil && len(others) == 0 {
		return nil, nil
	}

	predicted := map[string]any{}
	if err := json.Unmarshal(diffRes.PredictedLive, &predicted); err != nil {
		return nil, fmt.Errorf("failed to unmarshal predicted live: %w", err)
	}

	var retained []retainedField
	var removals []fieldpath.Path
	seen := map[string]bool{}

	if applied != nil {
		applied.Iterate(func(p fieldpath.Path) {
			if hasPath(target.Object, p) {
				return
			}
			if owners := ownersOf(others, p); len(owners) > 0 {
				retained = append(retained, retainedField{path: p.Copy(), owners: owners})
				return
			}
			r := removalPoint(target.Object, p)
			if ownedUnder(others, r) {
				r = p
			}
			if !seen[r.String()] {
				seen[r.String()] = true
				removals = append(removals, r.Copy())
			}
		})
	}

	for _, o := range others {
		if !isArgoCDResidue(o.owner) {
			continue
		}
		o.set.Iterate(func(p fieldpath.Path) {
			if hasPath(target.Object, p) || applied != nil && applied.Has(p) {
				return
			}
			retained = append(retained, retainedField{path: p.Copy(), owners: ownersOf(others, p)})
		})
	}

	modified := false
	for _, r := range removals {
		if removePath(predicted, r) {
			modified = true
		}
	}
	if modified {
		raw, err := json.Marshal(predicted)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal predicted live: %w", err)
		}
		diffRes.PredictedLive = raw
		diffRes.Modified = true
	}

	return dedupeRetained(retained), nil
}

func splitManagedFields(entries []metav1.ManagedFieldsEntry) (*fieldpath.Set, []ownedSet, error) {
	var applied *fieldpath.Set
	var others []ownedSet
	for _, e := range entries {
		if e.FieldsV1 == nil || e.Subresource != "" {
			continue
		}
		s := &fieldpath.Set{}
		if err := s.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil && err != io.EOF {
			return nil, nil, fmt.Errorf("failed to decode managed fields of %s: %w", e.Manager, err)
		}
		if e.Manager == argoCDSSAManager && e.Operation == metav1.ManagedFieldsOperationApply {
			applied = s
			continue
		}
		others = append(others, ownedSet{owner: fmt.Sprintf("%s/%s", e.Manager, e.Operation), set: s})
	}
	return applied, others, nil
}

// isArgoCDResidue matches ownership ArgoCD left behind through a client-side
// sync. Those fields came from git, so their absence from the manifest means
// they were removed from git, yet SSA will never delete them.
func isArgoCDResidue(owner string) bool {
	return owner == argoCDSSAManager+"/"+string(metav1.ManagedFieldsOperationUpdate) ||
		owner == "argocd-server/"+string(metav1.ManagedFieldsOperationUpdate)
}

func ownersOf(others []ownedSet, p fieldpath.Path) []string {
	var owners []string
	for _, o := range others {
		if o.set.Has(p) {
			owners = append(owners, o.owner)
		}
	}
	return owners
}

func ownedUnder(others []ownedSet, prefix fieldpath.Path) bool {
	for _, o := range others {
		found := false
		o.set.Iterate(func(p fieldpath.Path) {
			if !found && hasPrefix(p, prefix) {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

func hasPrefix(p, prefix fieldpath.Path) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if !prefix[i].Equals(p[i]) {
			return false
		}
	}
	return true
}

// removalPoint returns the shortest prefix of p that target lacks, so that a
// whole removed block is dropped instead of leaving empty parents behind.
func removalPoint(target map[string]any, p fieldpath.Path) fieldpath.Path {
	for i := 1; i <= len(p); i++ {
		if !hasPath(target, p[:i]) {
			return p[:i]
		}
	}
	return p
}

func hasPath(obj map[string]any, p fieldpath.Path) bool {
	var cur any = obj
	for _, pe := range p {
		next, ok := step(cur, pe)
		if !ok {
			return false
		}
		cur = next
	}
	return true
}

func step(cur any, pe fieldpath.PathElement) (any, bool) {
	switch {
	case pe.FieldName != nil:
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[*pe.FieldName]
		return v, ok
	default:
		l, ok := cur.([]any)
		if !ok {
			return nil, false
		}
		i := listIndex(l, pe)
		if i < 0 {
			return nil, false
		}
		return l[i], true
	}
}

func listIndex(l []any, pe fieldpath.PathElement) int {
	for i, item := range l {
		switch {
		case pe.Key != nil:
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			match := true
			for _, f := range *pe.Key {
				v, ok := m[f.Name]
				if !ok || !value.Equals(value.NewValueInterface(v), f.Value) {
					match = false
					break
				}
			}
			if match {
				return i
			}
		case pe.Value != nil:
			if value.Equals(value.NewValueInterface(item), *pe.Value) {
				return i
			}
		case pe.Index != nil:
			if *pe.Index == i {
				return i
			}
		}
	}
	return -1
}

func removePath(obj map[string]any, p fieldpath.Path) bool {
	if len(p) == 0 {
		return false
	}
	var parent any = obj
	for _, pe := range p[:len(p)-1] {
		next, ok := step(parent, pe)
		if !ok {
			return false
		}
		parent = next
	}

	last := p[len(p)-1]
	if last.FieldName != nil {
		m, ok := parent.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := m[*last.FieldName]; !ok {
			return false
		}
		delete(m, *last.FieldName)
		return true
	}

	l, ok := parent.([]any)
	if !ok {
		return false
	}
	i := listIndex(l, last)
	if i < 0 {
		return false
	}
	return setInParent(obj, p[:len(p)-1], append(l[:i:i], l[i+1:]...))
}

func setInParent(obj map[string]any, p fieldpath.Path, v any) bool {
	if len(p) == 0 {
		return false
	}
	var parent any = obj
	for _, pe := range p[:len(p)-1] {
		next, ok := step(parent, pe)
		if !ok {
			return false
		}
		parent = next
	}
	last := p[len(p)-1]
	if last.FieldName != nil {
		m, ok := parent.(map[string]any)
		if !ok {
			return false
		}
		m[*last.FieldName] = v
		return true
	}
	l, ok := parent.([]any)
	if !ok {
		return false
	}
	i := listIndex(l, last)
	if i < 0 {
		return false
	}
	l[i] = v
	return true
}

func dedupeRetained(in []retainedField) []retainedField {
	byPath := map[string]*retainedField{}
	var order []string
	for _, r := range in {
		k := r.path.String()
		if existing, ok := byPath[k]; ok {
			existing.owners = append(existing.owners, r.owners...)
			continue
		}
		rc := r
		byPath[k] = &rc
		order = append(order, k)
	}
	sort.Strings(order)
	out := make([]retainedField, 0, len(order))
	for _, k := range order {
		r := byPath[k]
		r.owners = uniqueSorted(r.owners)
		out = append(out, *r)
	}
	return out
}

func uniqueSorted(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func writeRetained(w io.Writer, retained []retainedField) {
	for _, r := range retained {
		fmt.Fprintf(w, "! removed from git but kept on sync, still owned by %s: %s\n", strings.Join(r.owners, ", "), r.path.String())
	}
}
