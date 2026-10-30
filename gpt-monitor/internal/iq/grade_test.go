package iq

import (
	"strings"
	"testing"
)

func TestGradeTakesLastNumber(t *testing.T) {
	cases := []struct {
		answer  string
		outcome string
		value   string
	}{
		{"21", OutcomeCorrect, "21"},
		{"最坏情况：先摸出 7 个苹果星、4 个西瓜星…再加 9 个圆，所以最少需要 **21** 个。", OutcomeCorrect, "21"},
		{"答案：二十一个", OutcomeCorrect, "21"},
		{"$\\boxed{21}$", OutcomeCorrect, "21"},
		{"最少取出 20 个即可。", OutcomeWrong, "20"},
		{"先算 21，但考虑西瓜后应为 23 个", OutcomeWrong, "23"},
		{"十二", OutcomeWrong, "12"},
		{"我无法确定", OutcomeNoAnswer, ""},
		{"", OutcomeNoAnswer, ""},
		// 列表序号不应被当成最终答案
		{"所以答案是 21 个。\n注意：\n1. 形状可以靠手感分辨", OutcomeCorrect, "21"},
		{"一共要 1,021 个", OutcomeWrong, "1021"},
	}
	for _, c := range cases {
		outcome, value := Grade(c.answer, 21)
		if outcome != c.outcome || value != c.value {
			t.Errorf("Grade(%q) = (%s, %s), want (%s, %s)", c.answer, outcome, value, c.outcome, c.value)
		}
	}
}

func TestGradeAnswerPrefersJSON(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		outcome   string
		value     string
		format    string
		reasoning string
	}{
		{"纯 JSON", `{"answer": 21, "reasoning": "圆 9 + 星 12"}`, OutcomeCorrect, "21", FormatJSON, "圆 9 + 星 12"},
		{"代码块包裹", "```json\n{\"answer\": 21, \"reasoning\": \"最坏情况\"}\n```", OutcomeCorrect, "21", FormatJSON, "最坏情况"},
		{"前后有废话，推理里的数字不干扰", "思考：先拿 7 个苹果星……\n{\"answer\": 20, \"reasoning\": \"7+4+9=20，再加 1 个是 21\"}", OutcomeWrong, "20", FormatJSON, "7+4+9=20，再加 1 个是 21"},
		{"字符串数字", `{"answer": "21个"}`, OutcomeCorrect, "21", FormatJSON, ""},
		{"浮点整数", `{"answer": 21.0}`, OutcomeCorrect, "21", FormatJSON, ""},
		{"取最后一个 JSON", `{"answer": 23} 更正：{"answer": 21}`, OutcomeCorrect, "21", FormatJSON, ""},
		{"answer 为空", `{"answer": null, "reasoning": "不确定"}`, OutcomeNoAnswer, "", FormatJSON, "不确定"},
		{"reasoning 里有括号", `{"reasoning": "取 {圆} 和 {星}", "answer": 21}`, OutcomeCorrect, "21", FormatJSON, "取 {圆} 和 {星}"},
		{"没给 JSON 退回取数", "所以最少需要 21 个。", OutcomeCorrect, "21", FormatText, ""},
		{"JSON 没有 answer 字段时退回取数", `{"result": 20} 最终答案 21`, OutcomeCorrect, "21", FormatText, ""},
		{"什么都没有", "我不知道", OutcomeNoAnswer, "", FormatNone, ""},
	}
	for _, c := range cases {
		got := GradeAnswer(c.text, 21)
		if got.Outcome != c.outcome || got.Value != c.value || got.Format != c.format || got.Reasoning != c.reasoning {
			t.Errorf("%s: got %+v, want outcome=%s value=%s format=%s reasoning=%q", c.name, got, c.outcome, c.value, c.format, c.reasoning)
		}
	}
}

func TestCandyQuestionAsksForJSON(t *testing.T) {
	if !strings.Contains(CandyQuestion.Prompt, `{"answer": <整数>`) || CandyQuestion.Answer != 21 {
		t.Fatalf("题目应要求 JSON 作答: %s", CandyQuestion.Prompt)
	}
}

func TestParseChineseNumber(t *testing.T) {
	cases := map[string]int{"二十一": 21, "十二": 12, "十": 10, "一百零五": 105, "两千": 2000, "零": 0, "九": 9}
	for in, want := range cases {
		got, ok := parseChineseNumber(in)
		if !ok || got != want {
			t.Errorf("parseChineseNumber(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	if _, ok := parseChineseNumber("百千"); ok {
		t.Errorf("「百千」不是合法数字，但被解析了")
	}
}
