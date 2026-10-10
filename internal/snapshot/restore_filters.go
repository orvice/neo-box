package snapshot

import (
	"encoding/json"
	"fmt"
)

// Filter keys copied as they are for both views and webhooks.
var filterKeys = []string{"comparison_op", "comparison_sub_op", "value", "logical_op", "enabled", "meta"}

// restoreFilters re-creates a captured filter tree, groups before their
// members. create binds the destination view or hook; skipped reports a
// loss with its snapshot field ID, when applicable. Missing fields and
// rejected groups (including their descendants) are dropped with warnings.
func (r *restorer) restoreFilters(filters []json.RawMessage, create func(any) (string, error), skipped func(field, msg string)) error {
	created := map[string]string{} // snapshot filter id -> new id
	for _, f := range filters {
		parent := jsonString(f, "fk_parent_id")
		if parent != "" && created[parent] == "" {
			skipped("", "a filter in a group that was not restored")
			continue
		}
		var body map[string]any
		if jsonBool(f, "is_group") {
			body = map[string]any{"is_group": true}
		} else {
			var missing string
			body, missing = r.remapRefs(f, "fk_column_id", "fk_link_col_id", "fk_value_col_id", "fk_parent_column_id")
			if missing != "" {
				skipped(missing, "a filter on a field that was not restored")
				continue
			}
		}
		for _, key := range filterKeys {
			if val := jsonValue(f, key); val != nil {
				body[key] = val
			}
		}
		if parent != "" {
			body["fk_parent_id"] = created[parent]
		}
		newID, err := create(body)
		if err != nil {
			if !rejected(err) {
				return fmt.Errorf("filter: %w", err)
			}
			skipped("", "filter: "+apiMessage(err))
			continue
		}
		created[jsonString(f, "id")] = newID
	}
	return nil
}
