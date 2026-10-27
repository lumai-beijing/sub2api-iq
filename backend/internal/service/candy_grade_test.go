package service

import (
	"strings"
	"testing"
)

func TestCandyGradeTakesLastNumber(t *testing.T) {
	cases := []struct {
		answer  string
		outcome string
		value   string
	}{
		{"21", CandyOutcomeCorrect, "21"},
		{"最坏情况：先摸出 7 个苹果星、4 个西瓜星…再加 9 个圆，所以最少需要 **21** 个。", CandyOutcomeCorrect, "21"},
		{"答案：二十一个", CandyOutcomeCorrect, "21"},
		{"$\\boxed{21}$", CandyOutcomeCorrect, "21"},
		{"最少取出 20 个即可。", CandyOutcomeWrong, "20"},
		{"先算 21，但考虑西瓜后应为 23 个", CandyOutcomeWrong, "23"},
		{"十二", CandyOutcomeWrong, "12"},
		{"我无法确定", CandyOutcomeNoAnswer, ""},
		{"", CandyOutcomeNoAnswer, ""},
		// 列表序号不应被当成最终答案
		{"所以答案是 21 个。\n注意：\n1. 形状可以靠手感分辨", CandyOutcomeCorrect, "21"},
		{"一共要 1,021 个", CandyOutcomeWrong, "1021"},
	}
	for _, c := range cases {
		outcome, value := candyGrade(c.answer, 21)
		if outcome != c.outcome || value != c.value {
			t.Errorf("candyGrade(%q) = (%s, %s), want (%s, %s)", c.answer, outcome, value, c.outcome, c.value)
		}
	}
}

func TestGradeCandyAnswerPrefersJSON(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		outcome   string
		value     string
		format    string
		reasoning string
	}{
		{"纯 JSON", `{"answer": 21, "reasoning": "圆 9 + 星 12"}`, CandyOutcomeCorrect, "21", CandyFormatJSON, "圆 9 + 星 12"},
		{"代码块包裹", "```json\n{\"answer\": 21, \"reasoning\": \"最坏情况\"}\n```", CandyOutcomeCorrect, "21", CandyFormatJSON, "最坏情况"},
		{"前后有废话，推理里的数字不干扰", "思考：先拿 7 个苹果星……\n{\"answer\": 20, \"reasoning\": \"7+4+9=20，再加 1 个是 21\"}", CandyOutcomeWrong, "20", CandyFormatJSON, "7+4+9=20，再加 1 个是 21"},
		{"字符串数字", `{"answer": "21个"}`, CandyOutcomeCorrect, "21", CandyFormatJSON, ""},
		{"浮点整数", `{"answer": 21.0}`, CandyOutcomeCorrect, "21", CandyFormatJSON, ""},
		{"取最后一个 JSON", `{"answer": 23} 更正：{"answer": 21}`, CandyOutcomeCorrect, "21", CandyFormatJSON, ""},
		{"answer 为空", `{"answer": null, "reasoning": "不确定"}`, CandyOutcomeNoAnswer, "", CandyFormatJSON, "不确定"},
		{"reasoning 里有括号", `{"reasoning": "取 {圆} 和 {星}", "answer": 21}`, CandyOutcomeCorrect, "21", CandyFormatJSON, "取 {圆} 和 {星}"},
		{"没给 JSON 退回取数", "所以最少需要 21 个。", CandyOutcomeCorrect, "21", CandyFormatText, ""},
		{"JSON 没有 answer 字段时退回取数", `{"result": 20} 最终答案 21`, CandyOutcomeCorrect, "21", CandyFormatText, ""},
		{"什么都没有", "我不知道", CandyOutcomeNoAnswer, "", CandyFormatNone, ""},
	}
	for _, c := range cases {
		got := GradeCandyAnswer(c.text, 21)
		if got.Outcome != c.outcome || got.Value != c.value || got.Format != c.format || got.Reasoning != c.reasoning {
			t.Errorf("%s: got %+v, want outcome=%s value=%s format=%s reasoning=%q", c.name, got, c.outcome, c.value, c.format, c.reasoning)
		}
	}
}

func TestCandyQuestionAsksForJSON(t *testing.T) {
	prompt := OriginalCandyVariant().Prompt
	if !strings.Contains(prompt, "answer") || !strings.Contains(prompt, "JSON") {
		t.Fatalf("题目应要求 JSON 作答: %s", prompt)
	}
}

func TestCandyParseChineseNumber(t *testing.T) {
	cases := map[string]int{"二十一": 21, "十二": 12, "十": 10, "一百零五": 105, "两千": 2000, "零": 0, "九": 9}
	for in, want := range cases {
		got, ok := candyParseChineseNumber(in)
		if !ok || got != want {
			t.Errorf("candyParseChineseNumber(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	if _, ok := candyParseChineseNumber("百千"); ok {
		t.Errorf("「百千」不是合法数字，但被解析了")
	}
}
