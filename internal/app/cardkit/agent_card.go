package cardkit

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PrepareAgentCard copies a CardKit 2.0 request and collects explicit callback
// values for signing. Form fields submit together through the submit button.
func PrepareAgentCard(input map[string]any) (map[string]any, []map[string]any, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > 100*1024 {
		return nil, nil, errors.New("agent card exceeds 100 KB")
	}
	var card map[string]any
	if err := json.Unmarshal(raw, &card); err != nil {
		return nil, nil, err
	}
	if card["schema"] != "2.0" {
		return nil, nil, errors.New("agent card requires schema 2.0")
	}
	body, ok := card["body"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("agent card requires body")
	}
	var values []map[string]any
	var walk func(any, bool) error
	walk = func(node any, inForm bool) error {
		switch n := node.(type) {
		case []any:
			for _, item := range n {
				if err := walk(item, inForm); err != nil {
					return err
				}
			}
		case map[string]any:
			tag, _ := n["tag"].(string)
			if tag == "form" {
				inForm = true
			}
			if tag == "input" || tag == "select_static" || tag == "multi_select_static" || tag == "select_person" || tag == "multi_select_person" || tag == "date_picker" || tag == "picker_time" || tag == "picker_datetime" {
				if !inForm {
					return fmt.Errorf("%s must be inside a form", tag)
				}
			}
			if n["form_action_type"] == "submit" && n["behaviors"] == nil {
				n["behaviors"] = []any{map[string]any{"type": "callback", "value": map[string]any{}}}
			}
			if behaviors, ok := n["behaviors"].([]any); ok {
				for _, item := range behaviors {
					b, ok := item.(map[string]any)
					if !ok {
						return errors.New("invalid card behavior")
					}
					if b["type"] != "callback" {
						continue
					}
					if tag != "button" {
						return errors.New("agent callbacks require buttons")
					}
					value, ok := b["value"].(map[string]any)
					if !ok {
						if b["value"] != nil {
							return errors.New("callback value must be an object")
						}
						value = map[string]any{}
						b["value"] = value
					}
					for _, key := range []string{"cmd", "__bridge_cb", "__claude_cb", "bridge_token", "form_value"} {
						if _, exists := value[key]; exists {
							return fmt.Errorf("reserved callback field: %s", key)
						}
					}
					values = append(values, value)
				}
			}
			for _, key := range []string{"elements", "columns"} {
				if child, exists := n[key]; exists {
					if err := walk(child, inForm); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(body, false); err != nil {
		return nil, nil, err
	}
	if len(values) == 0 {
		return nil, nil, errors.New("agent card requires a callback button")
	}
	return card, values, nil
}
