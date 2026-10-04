package snapshot

import (
	"context"
	"fmt"
	"strings"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

// linkInfo reads a link field's relation type and related table.
func linkInfo(f *planField) (relType, relatedTable string) {
	return jsonString(f.Options, "relation_type"), jsonString(f.Options, "related_table_id")
}

// mirrorRelation is the relation type seen from the other side.
func mirrorRelation(t string) string {
	switch t {
	case "hm":
		return "bt"
	case "bt":
		return "hm"
	case "om":
		return "mo"
	case "mo":
		return "om"
	}
	return t
}

// createRelations creates every relation once. Snapshots hold both sides
// of a relation, but NocoDB always creates the inverse field itself, so the
// restore creates one side, finds the inverse NocoDB added, and renames it
// to the snapshot's title. Self-links have no inverse.
func (r *restorer) createRelations(ctx context.Context) error {
	r.sides = map[string]bool{}
	pairs, err := r.ambiguousLinkPairs()
	if err != nil {
		return err
	}
	handled := map[string]bool{}
	for _, t := range r.tables {
		for _, f := range t.fields {
			if f.kind != kindLink || f.skipReason != "" || handled[f.ID] {
				continue
			}
			relType, relatedID := linkInfo(f)
			rt := r.byID[relatedID]
			if rt == nil || rt.newID == "" {
				f.skipReason = "its related table is not in the snapshot"
				handled[f.ID] = true
				continue
			}
			inv := r.findInverse(t, f, rt, handled, pairs)
			side, sideTable, other, otherTable := f, t, inv, rt
			// Create from the parent side, so NocoDB's view of which
			// table owns the relation matches the snapshot.
			if inv != nil && (relType == "bt" || relType == "mo") {
				side, sideTable, other, otherTable = inv, rt, f, t
			}
			handled[side.ID] = true
			if other != nil {
				handled[other.ID] = true
			}
			if err := r.createRelation(ctx, sideTable, side, otherTable, other); err != nil {
				return err
			}
		}
	}
	return nil
}

// inverseCandidates lists the fields of rt that could be the other side of
// f: links back to t with the mirrored relation type. A self-link has none.
func (r *restorer) inverseCandidates(t *planTable, f *planField, rt *planTable, handled map[string]bool) []*planField {
	if rt == t {
		return nil
	}
	relType, _ := linkInfo(f)
	var out []*planField
	for _, g := range rt.fields {
		if g.kind != kindLink || g.skipReason != "" || handled[g.ID] {
			continue
		}
		gType, gRelated := linkInfo(g)
		if gRelated == t.id && gType == mirrorRelation(relType) {
			out = append(out, g)
		}
	}
	return out
}

// findInverse picks the other side of f. Field options don't name the
// inverse, so when two tables share several relations of the same type,
// the candidate whose links mirror f's links wins; with no links to tell
// them apart, the first candidate is taken and a warning is recorded.
func (r *restorer) findInverse(t *planTable, f *planField, rt *planTable, handled map[string]bool, pairs map[string]map[string]bool) *planField {
	cands := r.inverseCandidates(t, f, rt, handled)
	if len(cands) <= 1 {
		if len(cands) == 0 {
			return nil
		}
		return cands[0]
	}
	best, bestScore := cands[0], -1
	for _, g := range cands {
		score := 0
		for p := range pairs[f.ID] {
			from, to, _ := strings.Cut(p, ">")
			if pairs[g.ID][to+">"+from] {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = g, score
		}
	}
	if bestScore == 0 {
		r.warn(WarnRelationGuessed, t.title, f.Title, 1, fmt.Sprintf(
			"paired with %q in %q by order: %d relations link these tables and the snapshot's links don't tell them apart",
			best.Title, rt.title, len(cands)))
	}
	return best
}

// ambiguousLinkPairs reads the links of every link field whose inverse is
// ambiguous, as "from>to" record ID pairs. It costs an extra pass over the
// snapshot only when such fields exist.
func (r *restorer) ambiguousLinkPairs() (map[string]map[string]bool, error) {
	need := map[string]bool{}
	for _, t := range r.tables {
		for _, f := range t.fields {
			if f.kind != kindLink {
				continue
			}
			_, relatedID := linkInfo(f)
			rt := r.byID[relatedID]
			if rt == nil {
				continue
			}
			if cands := r.inverseCandidates(t, f, rt, nil); len(cands) > 1 {
				need[f.ID] = true
				for _, g := range cands {
					need[g.ID] = true
				}
			}
		}
	}
	if len(need) == 0 {
		return nil, nil
	}
	r.opts.Progress("matching relations")
	pairs := map[string]map[string]bool{}
	_, err := r.read(func(st *Table) error {
		for _, l := range st.Links {
			if !need[l.FieldID] {
				continue
			}
			set := pairs[l.FieldID]
			if set == nil {
				set = map[string]bool{}
				pairs[l.FieldID] = set
			}
			from := nocodb.RawIDString(l.RecordID)
			for _, to := range l.LinkedIDs {
				set[from+">"+nocodb.RawIDString(to)] = true
			}
		}
		return nil
	})
	return pairs, err
}

func (r *restorer) createRelation(ctx context.Context, t *planTable, side *planField, rt *planTable, inverse *planField) error {
	nf, err := r.api.CreateField(ctx, r.baseID, t.newID, r.fieldDef(side))
	if err != nil {
		if !nocodb.IsBadRequest(err) {
			return fmt.Errorf("relation %q: %w", side.Title, err)
		}
		side.skipReason = apiMessage(err)
		if inverse != nil {
			inverse.skipReason = "the other side of its relation could not be created: " + side.skipReason
		}
		return nil
	}
	r.ids[side.ID] = nf.ID
	r.sides[side.ID] = true
	t.known[nf.ID] = true

	// NocoDB added the inverse to rt; it is the one link field there the
	// restore hasn't seen yet.
	related, err := r.api.GetTable(ctx, r.baseID, rt.newID)
	if err != nil {
		return fmt.Errorf("relation %q: read %q: %w", side.Title, rt.title, err)
	}
	var invID, invTitle string
	for _, f := range related.Fields {
		if !rt.known[f.ID] && f.IsLink() {
			invID, invTitle = f.ID, f.Title
		}
		rt.known[f.ID] = true
	}
	if inverse == nil {
		return nil
	}
	if invID == "" {
		inverse.skipReason = "NocoDB did not create the other side of its relation"
		return nil
	}
	r.ids[inverse.ID] = invID
	if invTitle != inverse.Title {
		if err := r.api.UpdateField(ctx, r.baseID, invID, map[string]string{"title": inverse.Title}); err != nil {
			if !nocodb.IsBadRequest(err) {
				return fmt.Errorf("rename %q: %w", inverse.Title, err)
			}
			r.warn(WarnFieldSkipped, rt.title, inverse.Title, 1,
				fmt.Sprintf("restored under NocoDB's name %q: %s", invTitle, apiMessage(err)))
		}
	}
	return nil
}

// createVirtualFields creates lookups, rollups, formulas and the like in
// passes: a field waits until the fields its options reference exist, and
// a field NocoDB rejects is retried in the next pass, since formulas
// reference other fields by title and may need one created later. Fields
// still pending when a pass makes no progress are skipped.
func (r *restorer) createVirtualFields(ctx context.Context) error {
	type pending struct {
		t       *planTable
		f       *planField
		lastErr string
	}
	var queue []*pending
	for _, t := range r.tables {
		for _, f := range t.fields {
			if f.kind == kindVirtual && f.skipReason == "" && t.newID != "" {
				queue = append(queue, &pending{t: t, f: f})
			}
		}
	}
	for len(queue) > 0 {
		var next []*pending
		for _, p := range queue {
			if refs := r.unresolvedRefs(p.f); len(refs) > 0 {
				p.lastErr = "it depends on a field that was not restored"
				next = append(next, p)
				continue
			}
			nf, err := r.api.CreateField(ctx, r.baseID, p.t.newID, r.fieldDef(p.f))
			if err != nil {
				if !nocodb.IsBadRequest(err) {
					return fmt.Errorf("field %q: %w", p.f.Title, err)
				}
				p.lastErr = apiMessage(err)
				next = append(next, p)
				continue
			}
			r.ids[p.f.ID] = nf.ID
			p.t.known[nf.ID] = true
		}
		if len(next) == len(queue) {
			for _, p := range next {
				p.f.skipReason = p.lastErr
			}
			break
		}
		queue = next
	}
	return nil
}

// setDisplayFields points each table's display value at the snapshot's
// display field.
func (r *restorer) setDisplayFields(ctx context.Context) error {
	for _, t := range r.tables {
		if t.newID == "" || t.displayField == "" {
			continue
		}
		newID := r.ids[t.displayField]
		if newID == "" {
			r.warn(WarnDisplayField, t.title, "", 1, "the display field was not restored")
			continue
		}
		err := r.api.UpdateTable(ctx, r.baseID, t.newID, map[string]string{"display_field_id": newID})
		if err != nil {
			if !nocodb.IsBadRequest(err) {
				return fmt.Errorf("table %q: set display field: %w", t.title, err)
			}
			r.warn(WarnDisplayField, t.title, "", 1, apiMessage(err))
		}
	}
	return nil
}
