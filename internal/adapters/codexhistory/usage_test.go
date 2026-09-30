package codexhistory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUsageQuery(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fail, timeout bool
	}{{name: "success"}, {name: "error", fail: true}, {name: "timeout", timeout: true}} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{failList: tc.fail, noListResponse: tc.timeout}
			p := New(ProviderOptions{Runner: runner})
			inherit := false
			result, err := p.Usage(context.Background(), ListOptions{Binary: "codex", ProfileStateDir: "/state/profile", InheritCodexHome: &inherit, Timeout: 20 * time.Millisecond})
			if tc.fail || tc.timeout {
				if err == nil {
					t.Fatal("expected query error")
				}
				return
			}
			if err != nil || !strings.Contains(result, "已使用 25%") || !strings.Contains(result, "5 小时额度") {
				t.Fatalf("usage=%q error=%v", result, err)
			}
			if runner.requestsSnapshot()[1]["method"] != "account/rateLimits/read" {
				t.Fatal("incorrect method")
			}
			if got := envMap(runner.firstSpec(t).Env)["CODEX_HOME"]; !strings.Contains(got, "/state/profile") {
				t.Fatalf("CODEX_HOME=%q", got)
			}
		})
	}
}

func TestFormatUsageBucketsAndMissingData(t *testing.T) {
	result, err := formatUsage([]byte(`{"rateLimits":{"primary":{"usedPercent":99}},"rateLimitsByLimitId":{"codex":{"planType":"pro","secondary":{"usedPercent":42,"windowDurationMins":10080,"resetsAt":1700000000},"credits":{"balance":"12.50"}},"other":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"7 天额度：已使用 42%", "2023-11-14 22:13 UTC", "12.50", "未提供额度数据"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("missing %q: %s", expected, result)
		}
	}
	if strings.Contains(result, "99%") {
		t.Fatal("duplicated legacy bucket")
	}
	if _, err := formatUsage([]byte(`{}`)); err == nil {
		t.Fatal("expected missing response error")
	}
}
