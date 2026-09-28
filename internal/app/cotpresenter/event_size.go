package cotpresenter

import (
	"encoding/json"
	"fmt"
)

// 本地发送预算，包含事件字段与 JSON 转义；服务端精确上限尚未公开确认。
const eventWireBudget = 1024
const updateEventLimit = 16

func boundedEvents(eventType string, content any, timestamp int64) ([]Event, error) {
	payload, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}
	encode := func() (Event, bool, error) {
		data, err := json.Marshal(fields)
		if err != nil {
			return Event{}, false, err
		}
		event := Event{EventType: eventType, Content: string(data), Timestamp: timestamp}
		wire, err := json.Marshal(event)
		return event, len(wire) <= eventWireBudget, err
	}
	event, fits, err := encode()
	if err != nil || fits {
		return []Event{event}, err
	}
	switch eventType {
	case "TOOL_CALL_ARGS", "TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT":
		delta, ok := fields["delta"].(string)
		if !ok {
			break
		}
		runes := []rune(delta)
		var events []Event
		for len(runes) > 0 {
			low, high := 0, min(len(runes), eventWireBudget)
			for low < high {
				mid := low + (high-low+1)/2
				fields["delta"] = string(runes[:mid])
				_, fits, err := encode()
				if err != nil {
					return nil, err
				}
				if fits {
					low = mid
				} else {
					high = mid - 1
				}
			}
			if low == 0 {
				return nil, fmt.Errorf("COT 事件 %s 的固定字段超过发送预算", eventType)
			}
			fields["delta"] = string(runes[:low])
			event, _, err := encode()
			if err != nil {
				return nil, err
			}
			events = append(events, event)
			runes = runes[low:]
		}
		if len(events) > 0 {
			return events, nil
		}
	default:
		// 非增量事件只缩短展示文字，保留关联 ID 与事件结构。
		target, key := fields, ""
		switch eventType {
		case "RUN_STARTED":
			target, _ = fields["input"].(map[string]any)
			key = "query"
		case "TOOL_CALL_START":
			key = "title"
		case "TOOL_CALL_RESULT":
			key = "content"
		case "RUN_ERROR":
			key = "message"
		}
		if text, ok := target[key].(string); ok {
			runes := []rune(text)
			target[key] = "…"
			_, fits, err := encode()
			if err != nil {
				return nil, err
			}
			if fits {
				low, high := 0, min(len(runes), eventWireBudget)
				for low < high {
					mid := low + (high-low+1)/2
					target[key] = string(runes[:mid]) + "…"
					_, fits, err := encode()
					if err != nil {
						return nil, err
					}
					if fits {
						low = mid
					} else {
						high = mid - 1
					}
				}
				target[key] = string(runes[:low]) + "…"
				event, _, err := encode()
				return []Event{event}, err
			}
		}
	}
	return nil, fmt.Errorf("COT 事件 %s 超过本地发送预算 %d 字节", eventType, eventWireBudget)
}
