package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
)

// Hooks are the final pass and always created inactive. In particular,
// none of the restore's own writes can fire a webhook.
func (r *restorer) restoreHooks(ctx context.Context) error {
	n := 0
	_, err := r.read(func(st *Table) error {
		n++
		t := r.byID[st.ID]
		if t == nil || t.newID == "" || len(st.Hooks) == 0 {
			return nil
		}
		r.opts.Progress(fmt.Sprintf("[%d/%d] %s: webhooks", n, len(r.tables), t.title))
		for _, h := range st.Hooks {
			title := jsonString(h.Hook, "title")
			body, err := r.hookBody(h.Hook)
			if err != nil {
				r.warnHook(WarnHookSkipped, t.title, title, err.Error())
				continue
			}
			id, err := r.api.CreateHook(ctx, t.newID, body)
			if err != nil {
				if !rejected(err) {
					return fmt.Errorf("table %q hook %q: %w", t.title, title, err)
				}
				r.warnHook(WarnHookSkipped, t.title, title, apiMessage(err))
				continue
			}
			t.stats.HookCount++
			r.warn(WarnHooksDisabled, t.title, "", 1, "webhooks were turned off; turn them on in NocoDB when ready")
			notification := body["notification"].(map[string]any)
			if typ, _ := notification["type"].(string); typ != "URL" {
				r.warnHook(WarnHookNeedsSetup, t.title, title, typ+" notifications need the target instance's integration")
			}
			if err := r.restoreHookFilters(ctx, t, title, id, h.Filters); err != nil {
				return fmt.Errorf("table %q hook %q: %w", t.title, title, err)
			}
		}
		return nil
	})
	return err
}

// hookBody copies writable settings only. Notification is stored by v2
// as a JSON string, but creation accepts its decoded object. It is never
// scrubbed or remapped: headers and payload values may contain secrets.
func (r *restorer) hookBody(raw json.RawMessage) (map[string]any, error) {
	body := map[string]any{"active": false}
	for _, key := range []string{"title", "description", "event", "operation", "condition", "retries", "retry_interval", "timeout", "version", "env", "type", "async"} {
		if val := jsonValue(raw, key); val != nil {
			body[key] = val
		}
	}
	var notification map[string]any
	stored := jsonRaw(raw, "notification")
	if s, ok := jsonValue(raw, "notification").(string); ok {
		stored = json.RawMessage(s)
	}
	if err := json.Unmarshal(stored, &notification); err != nil || notification == nil {
		return nil, fmt.Errorf("invalid notification settings")
	}
	body["notification"] = notification
	if jsonValue(raw, "trigger_field") != nil {
		body["trigger_field"] = jsonBool(raw, "trigger_field")
	}
	if fields := jsonValue(raw, "trigger_fields"); fields != nil {
		var old []string
		if err := json.Unmarshal(jsonRaw(raw, "trigger_fields"), &old); err != nil {
			return nil, fmt.Errorf("invalid trigger fields")
		}
		mapped := make([]string, 0, len(old))
		for _, id := range old {
			if r.ids[id] == "" {
				return nil, fmt.Errorf("a trigger field was not restored")
			}
			mapped = append(mapped, r.ids[id])
		}
		body["trigger_fields"] = mapped
	}
	return body, nil
}

// restoreHookFilters creates groups before their members, remapping
// field and parent IDs as for view filters. A dropped condition is warned
// about; the hook remains inactive even if its conditions are incomplete.
func (r *restorer) restoreHookFilters(ctx context.Context, t *planTable, title, id string, filters []json.RawMessage) error {
	return r.restoreFilters(filters, func(body any) (string, error) {
		return r.api.CreateHookFilter(ctx, id, body)
	}, func(field, msg string) {
		if field != "" {
			msg = "field " + r.fieldTitle(t, field) + ": " + msg
		}
		r.warnHook(WarnHookSkipped, t.title, title, msg)
	})
}

// warnHook keeps different hooks and losses separate without treating a
// hook title as a field or view name.
func (r *restorer) warnHook(code, table, title, msg string) {
	msg = fmt.Sprintf("webhook %q: %s", title, msg)
	key := code + "\x00" + table + "\x00" + msg
	if w, ok := r.warnings[key]; ok {
		w.Count++
		return
	}
	r.warnings[key] = &Warning{Code: code, Table: table, Count: 1, Message: msg}
	r.warningOrder = append(r.warningOrder, key)
}
