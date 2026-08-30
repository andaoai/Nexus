package core

import "strings"

// MatchInput 创建匹配时的客户侧条件（由前端显式传入，不从文本抽取）。
type MatchInput struct {
	Budget       float64  // 客户预算（元）
	DesiredDays  int      // 客户期望交付天数
	DesiredStack []string // 客户期望技术栈
}

// MatchResult 匹配度结果，Score ∈ [0,100]，红黄绿灯由此映射。
type MatchResult struct {
	Score     int                `json:"score"`
	Breakdown map[string]float64 `json:"breakdown"` // budget/tech/time 各维度 0-1
}

const (
	wBudget = 0.40
	wTech   = 0.35
	wTime   = 0.25
)

// ComputeMatchScore 纯函数：预算 40% + 技术 35% + 时间 25%。
func ComputeMatchScore(in MatchInput, sol Solution) MatchResult {
	b := map[string]float64{
		"budget": budgetScore(in.Budget, sol.EstimatedCost),
		"tech":   techScore(in.DesiredStack, sol.TechStack),
		"time":   timeScore(in.DesiredDays, sol.DeliveryDays),
	}
	raw := b["budget"]*wBudget + b["tech"]*wTech + b["time"]*wTime
	return MatchResult{Score: int(raw*100 + 0.5), Breakdown: b}
}

// budgetScore ratio=budget/cost ∈ [0.8,1.2] 满分，线性衰减到 0.5/1.5 为 0。
func budgetScore(budget, cost float64) float64 {
	if budget <= 0 || cost <= 0 {
		return 0
	}
	r := budget / cost
	lo, hi := 0.8, 1.2
	switch {
	case r >= lo && r <= hi:
		return 1
	case r < lo:
		// 0.5 → 0 分，0.8 → 满分
		return clamp01((r - 0.5) / (lo - 0.5))
	default:
		// 1.2 → 满分，1.5 → 0 分
		return clamp01((1.5 - r) / (1.5 - hi))
	}
}

// techScore 交集占比；期望栈为空给 0.6 基准分。
func techScore(desired, provided []string) float64 {
	if len(desired) == 0 {
		return 0.6
	}
	set := map[string]bool{}
	for _, p := range provided {
		set[norm(p)] = true
	}
	hit := 0
	for _, d := range desired {
		if set[norm(d)] {
			hit++
		}
	}
	return float64(hit) / float64(len(desired))
}

// timeScore desired/delivery ≥1 满分，否则线性。
func timeScore(desiredDays, deliveryDays int) float64 {
	if deliveryDays <= 0 {
		return 0
	}
	if desiredDays <= 0 {
		return 0.5 // 未填期望周期，给中性分
	}
	return clamp01(float64(desiredDays) / float64(deliveryDays))
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// norm 技术栈名称归一化：小写去空格，使 "Java" 与 "java" 匹配。
func norm(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}
