package service

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// 判分结果。error 不计入准确率分母：它说明号或代理坏了，不是智商低。
const (
	CandyOutcomeCorrect  = "correct"
	CandyOutcomeWrong    = "wrong"
	CandyOutcomeNoAnswer = "no_answer"
)

// 作答格式。题目要求只输出 JSON：json = 按 answer 字段判；
// text = 没给 JSON，退回取正文最后一个数字；none = 什么都没解析出来。
const (
	CandyFormatJSON = "json"
	CandyFormatText = "text"
	CandyFormatNone = "none"
)

// CandyGrade 是一次作答的判分结果。
type CandyGrade struct {
	Outcome   string
	Value     string
	Format    string
	Reasoning string
}

// GradeCandyAnswer 先找带 answer 字段的 JSON 对象（取最后一个），按它判分；
// 没有 JSON 时退回 candyGrade 的「取最后一个数字」。
func GradeCandyAnswer(text string, expected int) CandyGrade {
	if answer, reasoning, found := candyLastAnswerObject(text); found {
		graded := CandyGrade{Format: CandyFormatJSON, Reasoning: candyTruncateRunes(strings.TrimSpace(reasoning), 300)}
		value, ok := candyJSONAnswerValue(answer)
		switch {
		case !ok:
			graded.Outcome = CandyOutcomeNoAnswer
		case value == strconv.Itoa(expected):
			graded.Outcome, graded.Value = CandyOutcomeCorrect, value
		default:
			graded.Outcome, graded.Value = CandyOutcomeWrong, value
		}
		return graded
	}
	outcome, value := candyGrade(text, expected)
	format := CandyFormatText
	if outcome == CandyOutcomeNoAnswer {
		format = CandyFormatNone
	}
	return CandyGrade{Outcome: outcome, Value: value, Format: format}
}

// candyLastAnswerObject 扫出文本里所有合法的 JSON 对象，返回最后一个带 answer 字段的。
// 不要求整段都是 JSON：模型常在前后多说一句，或者包一层 ```json 代码块。
func candyLastAnswerObject(text string) (answer json.RawMessage, reasoning string, found bool) {
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		end := candyMatchBrace(text, start)
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

// candyMatchBrace 返回与 text[start] 处 '{' 配对的 '}' 下标，跳过字符串里的括号；找不到返回 -1。
func candyMatchBrace(text string, start int) int {
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

// candyJSONAnswerValue 接受数字（21、21.0）或带数字的字符串（"21"、"21 个"、"二十一"）。
func candyJSONAnswerValue(raw json.RawMessage) (string, bool) {
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
		if value, ok := candyLastNumber(text); ok {
			return strconv.Itoa(value), true
		}
	}
	return "", false
}

// candyGrade 取答案文本里「最后一个」数字作为模型的最终答案。
// 推理型模型习惯先列中间量（7、9、12…）再给结论，取最后一个才是它的结论；
// 中文数字（二十一）也认。
func candyGrade(answer string, expected int) (outcome string, value string) {
	number, ok := candyLastNumber(answer)
	if !ok {
		return CandyOutcomeNoAnswer, ""
	}
	if number == expected {
		return CandyOutcomeCorrect, strconv.Itoa(number)
	}
	return CandyOutcomeWrong, strconv.Itoa(number)
}

// 阿拉伯数字（允许千分位逗号）或一段连续的中文数字。
var candyNumberPattern = regexp.MustCompile(`\d[\d,，]*|[零〇一二两三四五六七八九十百千]+`)

func candyLastNumber(text string) (int, bool) {
	text = candyStripMarkup(text)
	matches := candyNumberPattern.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if value, ok := candyParseNumber(matches[i]); ok {
			return value, true
		}
	}
	return 0, false
}

// 去掉 markdown/LaTeX 的修饰，免得 **21** 或 \boxed{21} 之类的写法干扰匹配；
// 也去掉形如 "1." "2、" 的列表序号，它们常出现在最后一段之后（例如「注意事项：1. ...」）。
var (
	candyMarkupPattern   = regexp.MustCompile(`\\(boxed|text|mathbf)\{([^}]*)\}`)
	candyListItemPattern = regexp.MustCompile(`(?m)^\s*\d+[.、)]\s+`)
)

func candyStripMarkup(text string) string {
	text = candyMarkupPattern.ReplaceAllString(text, "$2")
	return candyListItemPattern.ReplaceAllString(text, "")
}

func candyParseNumber(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	if raw[0] >= '0' && raw[0] <= '9' {
		digits := strings.NewReplacer(",", "", "，", "").Replace(raw)
		value, err := strconv.Atoi(digits)
		return value, err == nil
	}
	return candyParseChineseNumber(raw)
}

var candyChineseDigits = map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
var candyChineseUnits = map[rune]int{'十': 10, '百': 100, '千': 1000}

// candyParseChineseNumber 只覆盖本题需要的范围（< 10000）：二十一、十二、一百零五、两千。
func candyParseChineseNumber(raw string) (int, bool) {
	total, digit, seenDigit := 0, 0, false
	for _, r := range raw {
		if d, ok := candyChineseDigits[r]; ok {
			digit = d
			seenDigit = true
			continue
		}
		unit, ok := candyChineseUnits[r]
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

func candyTruncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
