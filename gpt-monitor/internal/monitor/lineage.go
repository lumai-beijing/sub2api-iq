package monitor

import (
	"strconv"
	"strings"

	"gpt-pro-monitor/internal/model"
)

// classifyResponseModel 与 sub2api 看门狗（PR #718）同一套规则：
// 把 "gpt-<版本>-<系列>" 拆谱系，同系列且版本不低于请求模型算升级，跨系列或降版算降智，
// 任一侧解析不了归 unknown（看门狗对它放行，看板单列不计入降智）。
func classifyResponseModel(requested, observed string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	observed = strings.ToLower(strings.TrimSpace(observed))
	if observed == "" {
		return model.ResponseBlank
	}
	if observed == requested {
		return model.ResponseSame
	}
	reqFamily, reqVersion, okReq := parseModelLineage(requested)
	obsFamily, obsVersion, okObs := parseModelLineage(observed)
	if !okReq || !okObs {
		return model.ResponseUnknown
	}
	if reqFamily != obsFamily || compareVersions(obsVersion, reqVersion) < 0 {
		return model.ResponseDegraded
	}
	return model.ResponseUpgraded
}

// ClassifyResponseModel 导出给智商检测用：判断「上游换模型」与降智看板同一套口径。
func ClassifyResponseModel(requested, observed string) string {
	return classifyResponseModel(requested, observed)
}

func parseModelLineage(name string) (family, version string, ok bool) {
	rest, found := strings.CutPrefix(strings.ToLower(strings.TrimSpace(name)), "gpt-")
	if !found {
		return "", "", false
	}
	version, family, found = strings.Cut(rest, "-")
	if !found || version == "" || family == "" {
		return "", "", false
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return "", "", false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return "", "", false
			}
		}
	}
	return family, version, true
}

func compareVersions(a, b string) int {
	pa, pb := semverParts(a), semverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func semverParts(v string) [3]int {
	var out [3]int
	for i, part := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		if i >= 3 {
			break
		}
		if n, err := strconv.Atoi(part); err == nil {
			out[i] = n
		}
	}
	return out
}
