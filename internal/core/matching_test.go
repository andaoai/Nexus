package core

import "testing"

func TestComputeMatchScore(t *testing.T) {
	sol := Solution{EstimatedCost: 280000, DeliveryDays: 60, TechStack: []string{"Java", "Spring Boot", "MySQL"}}

	cases := []struct {
		name  string
		input MatchInput
		min   int
		max   int
	}{
		{"预算/时间/技术全中", MatchInput{Budget: 300000, DesiredDays: 90, DesiredStack: []string{"Java", "MySQL"}}, 95, 100},
		{"预算正好区间内", MatchInput{Budget: 280000, DesiredDays: 60, DesiredStack: []string{"Java"}}, 93, 100},
		{"预算偏低", MatchInput{Budget: 140000, DesiredDays: 90, DesiredStack: []string{"Java"}}, 50, 70},
		{"预算减半", MatchInput{Budget: 140000, DesiredDays: 30, DesiredStack: []string{"Python"}}, 0, 45},
		{"期望栈为空给基准分", MatchInput{Budget: 280000, DesiredDays: 60}, 78, 95},
		{"未填期望天数给中性分", MatchInput{Budget: 280000, DesiredStack: []string{"Java"}}, 88, 98},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ComputeMatchScore(c.input, sol)
			if r.Score < c.min || r.Score > c.max {
				t.Errorf("score=%d, 期望 [%d,%d], breakdown=%v", r.Score, c.min, c.max, r.Breakdown)
			}
			for k, v := range r.Breakdown {
				if v < 0 || v > 1 {
					t.Errorf("breakdown[%s]=%v 越界", k, v)
				}
			}
		})
	}
}

func TestBudgetScoreEdges(t *testing.T) {
	cases := []struct {
		budget, cost, want float64
	}{
		{280000, 280000, 1},  // ratio 1.0
		{224000, 280000, 1},  // ratio 0.8 下界
		{336000, 280000, 1},  // ratio 1.2 上界
		{140000, 280000, 0},  // ratio 0.5
		{420000, 280000, 0},  // ratio 1.5
		{0, 280000, 0},       // 无预算
		{280000, 0, 0},       // 无报价
	}
	for _, c := range cases {
		got := budgetScore(c.budget, c.cost)
		if got != c.want {
			t.Errorf("budgetScore(%v,%v)=%v, want %v", c.budget, c.cost, got, c.want)
		}
	}
}

func TestTechScoreNormalize(t *testing.T) {
	if got := techScore([]string{"java"}, []string{"Java", "Go"}); got != 1 {
		t.Errorf("大小写归一化失败: %v", got)
	}
}
