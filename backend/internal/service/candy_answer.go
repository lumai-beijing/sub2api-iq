package service

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Match a final answer, not a substring of a different number or a discussion
// of possible answers. Formatting, common answer prefixes and candy units are
// allowed; contradictions, multiple answers and decimal numbers are not.
// The pattern is generated per number so 21 and 29 share exactly the same rules.
const candyAnswerPatternTemplate = `^(?:(?:最终)?(?:答案|结果)(?:为|是)?[:：]?|(?:最少|至少)(?:需要)?(?:取出|抽取|摸出)?|(?:需要|取出|抽取|摸出))?(?:%s|%s)(?:个|颗|粒)?(?:糖果|糖)?(?:即可)?[。.!！]?$|^(?:(?:the)?answer(?:is|:)?|atleast)?%s(?:candies)?[.!]?$`

var (
	candyAnswerPattern      = newCandyAnswerPattern("21", "二十一")
	candyAnswerIs29Pattern  = newCandyAnswerPattern("29", "二十九")
	candySplitNumberPattern = regexp.MustCompile(`[0-9０-９][\s\p{Zs}*_]+[0-9０-９]`)
)

func newCandyAnswerPattern(number, chineseNumber string) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(candyAnswerPatternTemplate, number, chineseNumber, number))
}

func CandyAnswerCorrect(output string) bool {
	return candyAnswerMatches(candyAnswerPattern, output)
}

func CandyAnswerIs29(output string) bool {
	return candyAnswerMatches(candyAnswerIs29Pattern, output)
}

func candyAnswerMatches(pattern *regexp.Regexp, output string) bool {
	if candySplitNumberPattern.MatchString(output) {
		return false
	}
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("*_`\"'“”", r) {
			return -1
		}
		if r >= '０' && r <= '９' {
			return '0' + r - '０'
		}
		return unicode.ToLower(r)
	}, output)
	return pattern.MatchString(normalized)
}
