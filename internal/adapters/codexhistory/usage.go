package codexhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type usageWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *int64   `json:"windowDurationMins"`
	ResetsAt           *int64   `json:"resetsAt"`
}

type usageSnapshot struct {
	LimitName string       `json:"limitName"`
	PlanType  string       `json:"planType"`
	Primary   *usageWindow `json:"primary"`
	Secondary *usageWindow `json:"secondary"`
	Credits   *struct {
		Balance    *string `json:"balance"`
		Unlimited  bool    `json:"unlimited"`
		HasCredits bool    `json:"hasCredits"`
	} `json:"credits"`
}

func (p *Provider) Usage(ctx context.Context, opts ListOptions) (string, error) {
	initialize, _ := json.Marshal(initializeRequest(p.clientInfo))
	request, _ := json.Marshal(rpcRequest{Method: "account/rateLimits/read", ID: 2, Params: nil})
	result, err := p.query(ctx, opts, string(initialize)+"\n"+string(request)+"\n")
	if err != nil {
		return "", err
	}
	return formatUsage(result)
}

func formatUsage(raw json.RawMessage) (string, error) {
	var response struct {
		RateLimits          *usageSnapshot           `json:"rateLimits"`
		RateLimitsByLimitID map[string]usageSnapshot `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	snapshots := response.RateLimitsByLimitID
	if len(snapshots) == 0 && response.RateLimits != nil {
		snapshots = map[string]usageSnapshot{"codex": *response.RateLimits}
	}
	if len(snapshots) == 0 {
		return "", fmt.Errorf("missing Codex rate limits")
	}
	keys := make([]string, 0, len(snapshots))
	for key := range snapshots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sections []string
	for _, key := range keys {
		snapshot := snapshots[key]
		name := snapshot.LimitName
		if name == "" {
			name = key
		}
		if snapshot.PlanType != "" {
			name += " (" + snapshot.PlanType + ")"
		}
		lines := []string{name}
		for index, window := range []*usageWindow{snapshot.Primary, snapshot.Secondary} {
			if window == nil || window.UsedPercent == nil {
				continue
			}
			label := []string{"主要额度", "次要额度"}[index]
			if window.WindowDurationMins != nil {
				minutes := *window.WindowDurationMins
				switch {
				case minutes > 0 && minutes%1440 == 0:
					label = fmt.Sprintf("%d 天额度", minutes/1440)
				case minutes > 0 && minutes%60 == 0:
					label = fmt.Sprintf("%d 小时额度", minutes/60)
				case minutes > 0:
					label = fmt.Sprintf("%d 分钟额度", minutes)
				}
			}
			line := fmt.Sprintf("%s：已使用 %g%%", label, *window.UsedPercent)
			if window.ResetsAt != nil {
				line += "；重置时间 " + time.Unix(*window.ResetsAt, 0).UTC().Format("2006-01-02 15:04 MST")
			}
			lines = append(lines, line)
		}
		if credits := snapshot.Credits; credits != nil {
			switch {
			case credits.Unlimited:
				lines = append(lines, "credits：无限额度")
			case credits.Balance != nil:
				lines = append(lines, "credits 余额："+*credits.Balance)
			case credits.HasCredits:
				lines = append(lines, "credits：可用")
			default:
				lines = append(lines, "credits：没有可用余额")
			}
		}
		if len(lines) == 1 {
			lines = append(lines, "未提供额度数据")
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n"), nil
}
