package snapshot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.orx.me/apps/neo-box/internal/nocodb"
)

type pendingRecord struct {
	oldID  string
	fields map[string]json.RawMessage
}

// restoreRecords inserts every table's records in snapshot order and maps
// each snapshot record ID to its new one.
func (r *restorer) restoreRecords(ctx context.Context) error {
	n := 0
	_, err := r.read(func(st *Table) error {
		n++
		t := r.byID[st.ID]
		if t == nil || t.newID == "" {
			return nil
		}
		var writable []*planField
		for _, f := range t.fields {
			if f.kind == kindPlain && f.restored(r.ids) {
				writable = append(writable, f)
			}
		}
		ids := make(map[string]json.RawMessage, len(st.Records))
		r.recordIDs[t.id] = ids
		prefix := fmt.Sprintf("[%d/%d] %s: ", n, len(r.tables), t.title)

		var (
			failed     int64
			firstErr   string
			examples   []string
			attachment = map[string]int64{}
			dropped    = map[string]int64{}
		)
		fail := func(p pendingRecord, err error) {
			failed++
			if firstErr == "" {
				firstErr = apiMessage(err)
			}
			if len(examples) < 5 {
				examples = append(examples, p.oldID)
			}
		}
		insert := func(batch []pendingRecord) error {
			fields := make([]map[string]json.RawMessage, len(batch))
			for i, p := range batch {
				fields[i] = p.fields
			}
			newIDs, err := r.api.InsertRecords(ctx, r.baseID, t.newID, fields)
			if err != nil {
				return err
			}
			for i, p := range batch {
				ids[p.oldID] = newIDs[i]
			}
			t.stats.RecordCount += int64(len(batch))
			return nil
		}
		flush := func(batch []pendingRecord) error {
			err := insert(batch)
			if err == nil {
				return nil
			}
			if !nocodb.IsBadRequest(err) {
				return err
			}
			// One bad record fails its whole batch; find it.
			if len(batch) == 1 {
				fail(batch[0], err)
				return nil
			}
			for _, p := range batch {
				if err := insert([]pendingRecord{p}); err != nil {
					if !nocodb.IsBadRequest(err) {
						return err
					}
					fail(p, err)
				}
			}
			return nil
		}

		batch := make([]pendingRecord, 0, r.opts.BatchSize)
		for i, rec := range st.Records {
			p := pendingRecord{oldID: rec.IDString(), fields: map[string]json.RawMessage{}}
			for _, f := range writable {
				v, ok := r.value(f, rec.Fields[f.Title], attachment, dropped)
				if ok {
					p.fields[f.Title] = v
				}
			}
			batch = append(batch, p)
			if len(batch) == r.opts.BatchSize {
				if err := flush(batch); err != nil {
					return fmt.Errorf("table %q: insert records: %w", t.title, err)
				}
				batch = batch[:0]
				r.opts.Progress(fmt.Sprintf("%s%d/%d records", prefix, i+1, len(st.Records)))
			}
		}
		if len(batch) > 0 {
			if err := flush(batch); err != nil {
				return fmt.Errorf("table %q: insert records: %w", t.title, err)
			}
		}

		if failed > 0 {
			r.warn(WarnRecordsFailed, t.title, "", failed,
				fmt.Sprintf("%s (e.g. record %s)", firstErr, strings.Join(examples, ", ")))
		}
		for _, f := range writable {
			if c := attachment[f.Title]; c > 0 {
				r.warn(WarnAttachmentsSkipped, t.title, f.Title, c, "attachments are not restored")
			}
			if c := dropped[f.Title]; c > 0 {
				r.warn(WarnUserValuesDropped, t.title, f.Title, c, "the user is not a member of the new base")
			}
		}
		return nil
	})
	return err
}

// value converts a snapshot value of f into what NocoDB accepts on insert.
// ok is false when nothing should be sent.
func (r *restorer) value(f *planField, v json.RawMessage, attachments, dropped map[string]int64) (json.RawMessage, bool) {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || bytes.Equal(v, []byte("null")) {
		return nil, false
	}
	switch f.Type {
	case "Attachment":
		var files []json.RawMessage
		if json.Unmarshal(v, &files) == nil {
			attachments[f.Title] += int64(len(files))
		}
		return nil, false
	case "User":
		users := userEmails(v)
		var keep []map[string]string
		for _, e := range users {
			if r.members[strings.ToLower(e)] {
				keep = append(keep, map[string]string{"email": e})
			} else {
				dropped[f.Title]++
			}
		}
		if len(keep) == 0 {
			return nil, false
		}
		out, _ := json.Marshal(keep)
		return out, true
	}
	return v, true
}

// userEmails reads the emails out of a User value: one user object or a
// list of them.
func userEmails(v json.RawMessage) []string {
	type user struct {
		Email string `json:"email"`
	}
	var list []user
	if json.Unmarshal(v, &list) != nil {
		var one user
		if json.Unmarshal(v, &one) != nil {
			return nil
		}
		list = []user{one}
	}
	var out []string
	for _, u := range list {
		if u.Email != "" {
			out = append(out, u.Email)
		}
	}
	return out
}

// restoreLinks re-creates the links captured on the side each relation was
// created from, translating both ends to the new record IDs.
func (r *restorer) restoreLinks(ctx context.Context) error {
	n := 0
	_, err := r.read(func(st *Table) error {
		n++
		t := r.byID[st.ID]
		if t == nil || t.newID == "" || len(st.Links) == 0 {
			return nil
		}
		prefix := fmt.Sprintf("[%d/%d] %s: ", n, len(r.tables), t.title)
		failed := map[string]int64{}
		firstErr := map[string]string{}
		for i, l := range st.Links {
			if !r.sides[l.FieldID] {
				continue
			}
			f := t.field(l.FieldID)
			_, relatedID := linkInfo(f)
			related := r.recordIDs[relatedID]
			from, ok := r.recordIDs[t.id][nocodb.RawIDString(l.RecordID)]
			if !ok {
				failed[f.Title] += int64(len(l.LinkedIDs))
				continue
			}
			var to []json.RawMessage
			seen := map[string]bool{}
			for _, id := range l.LinkedIDs {
				n, ok := related[nocodb.RawIDString(id)]
				if !ok {
					failed[f.Title]++
					continue
				}
				if !seen[string(n)] {
					seen[string(n)] = true
					to = append(to, n)
				}
			}
			for start := 0; start < len(to); start += r.opts.LinkBatchSize {
				chunk := to[start:min(start+r.opts.LinkBatchSize, len(to))]
				err := r.api.LinkRecords(ctx, r.baseID, t.newID, r.ids[f.ID], nocodb.RawIDString(from), chunk)
				if err != nil {
					if !nocodb.IsBadRequest(err) && !nocodb.IsNotFound(err) {
						return fmt.Errorf("table %q: link %q: %w", t.title, f.Title, err)
					}
					failed[f.Title] += int64(len(chunk))
					if firstErr[f.Title] == "" {
						firstErr[f.Title] = apiMessage(err)
					}
					continue
				}
				t.stats.LinkCount += int64(len(chunk))
			}
			if (i+1)%50 == 0 {
				r.opts.Progress(fmt.Sprintf("%slinks %d/%d", prefix, i+1, len(st.Links)))
			}
		}
		for _, f := range t.fields {
			if c := failed[f.Title]; c > 0 {
				msg := firstErr[f.Title]
				if msg == "" {
					msg = "a linked record was not restored"
				}
				r.warn(WarnLinksFailed, t.title, f.Title, c, msg)
			}
		}
		return nil
	})
	return err
}
