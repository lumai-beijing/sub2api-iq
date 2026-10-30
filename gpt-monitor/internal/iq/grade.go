package iq

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
)

// 判分结果。error 不计入准确率分母：它说明号或代理坏了，不是智商低。
const (
	OutcomeCorrect  = "correct"
	OutcomeWrong    = "wrong"
	OutcomeNoAnswer = "no_answer"
	OutcomeError    = "error"
)

// 作答格式。题目要求只输出 JSON：json = 按 answer 字段判；
// text = 没给 JSON，退回取正文最后一个数字；none = 什么都没解析出来。
const (
	FormatJSON = "json"
	FormatText = "text"
	FormatNone = "none"
)

// Question 是一道带标准答案的题。ID 进库，换题或改作答格式后新旧口径不会混在一起统计。
type Question struct {
	ID     string
	Prompt string // 原题，看板展示用
	Answer int
	// Render 每次发题随机改写叙述方式（条件、数字、答案都不变），见 variant.go。
	Render func(rng *rand.Rand) Variant
}

const candyQuestionText = "在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。" +
	"现已知不同口味的糖和不同形状的数量统计如下表。苹果味（圆形 7 / 五角星 7）、桃子味（圆形 9 / 五角星 6）、西瓜味（圆形 8 / 五角星 4），" +
	"参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？" +
	"（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）"

// 限定 JSON 作答：答案落在固定字段里，比从自由文本里猜数字可靠，采样详情也更好读。
const jsonAnswerInstruction = "\n\n请先完成推理，然后只输出一个 JSON 对象作为最终回答，不要输出 JSON 以外的任何文字，也不要用 Markdown 代码块包裹。格式：\n" +
	`{"answer": <整数>, "reasoning": "<不超过 80 字的推理要点>"}`

// CandyQuestion 是默认题。标准答案 21：圆形 9 个（桃子圆 9）+ 五角星 12 个（苹果星 7 + 西瓜星 4 + 桃子星 1），
// 已穷举验证最坏情况下 20 个不够、21 个必然满足。
// v1 是固定题面（2026-10-10 之前），v2 起每次随机改写叙述方式。
var CandyQuestion = Question{
	ID:     "candy-json-v2",
	Prompt: candyQuestionText + jsonAnswerInstruction,
	Answer: 21,
	Render: RenderCandy,
}

// 题面口径：统计时默认把固定题面和随机题面合在一起（同一道题、同一个答案），也可以只看其中一种。
const (
	QuestionScopeAll    = "all"
	QuestionScopeFixed  = "fixed"
	QuestionScopeRandom = "random"
)

// questionIDs 返回一个口径对应的题目 ID。未知口径按 all。
func questionIDs(scope string) []string {
	switch scope {
	case QuestionScopeFixed:
		return []string{fixedCandyQuestionID}
	case QuestionScopeRandom:
		return []string{CandyQuestion.ID}
	default:
		return []string{fixedCandyQuestionID, CandyQuestion.ID}
	}
}

// questionVariantKind 把题目 ID 翻成看板上的口径。
func questionVariantKind(id string) string {
	if id == fixedCandyQuestionID {
		return QuestionScopeFixed
	}
	return QuestionScopeRandom
}

// Graded 是一次作答的判分结果。
type Graded struct {
	Outcome   string
	Value     string
	Format    string
	Reasoning string
}

// GradeAnswer 先找带 answer 字段的 JSON 对象（取最后一个），按它判分；
// 没有 JSON 时退回 Grade 的「取最后一个数字」。
func GradeAnswer(text string, expected int) Graded {
	if answer, reasoning, found := lastAnswerObject(text); found {
		graded := Graded{Format: FormatJSON, Reasoning: truncateRunes(strings.TrimSpace(reasoning), 300)}
		value, ok := jsonAnswerValue(answer)
		switch {
		case !ok:
			graded.Outcome = OutcomeNoAnswer
		case value == strconv.Itoa(expected):
			graded.Outcome, graded.Value = OutcomeCorrect, value
		default:
			graded.Outcome, graded.Value = OutcomeWrong, value
		}
		return graded
	}
	outcome, value := Grade(text, expected)
	format := FormatText
	if outcome == OutcomeNoAnswer {
		format = FormatNone
	}
	return Graded{Outcome: outcome, Value: value, Format: format}
}

// lastAnswerObject 扫出文本里所有合法的 JSON 对象，返回最后一个带 answer 字段的。
// 不要求整段都是 JSON：模型常在前后多说一句，或者包一层 ```json 代码块。
func lastAnswerObject(text string) (answer json.RawMessage, reasoning string, found bool) {
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		end := matchBrace(text, start)
		if end < 0 {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text[start:end+1]), &object); err != nil {
			continue
		}
		raw, ok := object["answer"]
		if !ok {
			continue
		}
		answer, found = raw, true
		reasoning = ""
		if rawReasoning, ok := object["reasoning"]; ok {
			_ = json.Unmarshal(rawReasoning, &reasoning)
		}
		start = end
	}
	return answer, reasoning, found
}

// matchBrace 返回与 text[start] 处 '{' 配对的 '}' 下标，跳过字符串里的括号；找不到返回 -1。
func matchBrace(text string, start int) int {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// jsonAnswerValue 接受数字（21、21.0）或带数字的字符串（"21"、"21 个"、"二十一"）。
func jsonAnswerValue(raw json.RawMessage) (string, bool) {
	// json.Unmarshal 把 null 解进 float64 不报错、值留 0，不先挡住就会把「没答」判成「答了 0」。
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "null" {
		return "", false
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		if number == math.Trunc(number) && math.Abs(number) < 1e9 {
			return strconv.Itoa(int(number)), true
		}
		return strconv.FormatFloat(number, 'f', -1, 64), true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if value, ok := lastNumber(text); ok {
			return strconv.Itoa(value), true
		}
	}
	return "", false
}

// Grade 取答案文本里「最后一个」数字作为模型的最终答案。
// 推理型模型习惯先列中间量（7、9、12…）再给结论，取最后一个才是它的结论；
// 中文数字（二十一）也认。
func Grade(answer string, expected int) (outcome string, value string) {
	number, ok := lastNumber(answer)
	if !ok {
		return OutcomeNoAnswer, ""
	}
	if number == expected {
		return OutcomeCorrect, strconv.Itoa(number)
	}
	return OutcomeWrong, strconv.Itoa(number)
}

// 阿拉伯数字（允许千分位逗号）或一段连续的中文数字。
var numberPattern = regexp.MustCompile(`\d[\d,，]*|[零〇一二两三四五六七八九十百千]+`)

func lastNumber(text string) (int, bool) {
	text = stripMarkup(text)
	matches := numberPattern.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if value, ok := parseNumber(matches[i]); ok {
			return value, true
		}
	}
	return 0, false
}

// 去掉 markdown/LaTeX 的修饰，免得 **21** 或 \boxed{21} 之类的写法干扰匹配；
// 也去掉形如 "1." "2、" 的列表序号，它们常出现在最后一段之后（例如「注意事项：1. ...」）。
var (
	markupPattern   = regexp.MustCompile(`\\(boxed|text|mathbf)\{([^}]*)\}`)
	listItemPattern = regexp.MustCompile(`(?m)^\s*\d+[.、)]\s+`)
)

func stripMarkup(text string) string {
	text = markupPattern.ReplaceAllString(text, "$2")
	return listItemPattern.ReplaceAllString(text, "")
}

func parseNumber(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	if raw[0] >= '0' && raw[0] <= '9' {
		digits := strings.NewReplacer(",", "", "，", "").Replace(raw)
		value, err := strconv.Atoi(digits)
		return value, err == nil
	}
	return parseChineseNumber(raw)
}

var chineseDigits = map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
var chineseUnits = map[rune]int{'十': 10, '百': 100, '千': 1000}

// parseChineseNumber 只覆盖本题需要的范围（< 10000）：二十一、十二、一百零五、两千。
func parseChineseNumber(raw string) (int, bool) {
	total, digit, seenDigit := 0, 0, false
	for _, r := range raw {
		if d, ok := chineseDigits[r]; ok {
			digit = d
			seenDigit = true
			continue
		}
		unit, ok := chineseUnits[r]
		if !ok {
			return 0, false
		}
		if !seenDigit && unit == 10 {
			digit = 1 // 「十二」开头省略了「一」
		}
		total += digit * unit
		digit, seenDigit = 0, false
	}
	total += digit
	if total == 0 && !strings.ContainsAny(raw, "零〇") {
		return 0, false
	}
	return total, true
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
