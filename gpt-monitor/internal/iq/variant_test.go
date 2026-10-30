package iq

import (
	mrand "math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 每个槽位都选第 0 项，必须逐字还原原题：这保证了模板的骨架就是原题，随机化只是在它上面换说法。
func TestOriginalCandyIsTheOriginalQuestion(t *testing.T) {
	if got, want := OriginalCandy().Prompt, candyQuestionText+jsonAnswerInstruction; got != want {
		t.Fatalf("原题还原不一致\n got: %q\nwant: %q", got, want)
	}
	if CandyQuestion.Prompt != OriginalCandy().Prompt {
		t.Fatal("看板展示的原题应与第 0 项题面一致")
	}
}

var (
	clarifyAppleFirst = regexp.MustCompile(`圆形的?苹果(口)?味[^，。；]*?五角星形?的?桃子`)
	clarifyPeachFirst = regexp.MustCompile(`圆形的?桃子(口)?味[^，。；]*?五角星形?的?苹果`)
	ruleMarkers       = []string{"前决定", "开始前就定好", "前先确定", "摸之前", "开始前报出"}
	touchMarkers      = []string{"手感", "一摸"}
)

// 随机题面逐条核对：题目的每个条件都还在，答案不会混进题面。
func TestRenderCandyKeepsEveryCondition(t *testing.T) {
	rng := mrand.New(mrand.NewPCG(1, 2))
	for i := 0; i < 5000; i++ {
		v := RenderCandy(rng)
		p := v.Prompt
		fail := func(reason string) { t.Fatalf("第 %d 个题面%s（%s）：\n%s", i, reason, v.Signature, p) }
		if !strings.Contains(p, "黑") {
			fail("丢了「黑色袋子」")
		}
		for _, word := range []string{"苹果", "桃子", "西瓜", "圆形", "五角星", "三种"} {
			if !strings.Contains(p, word) {
				fail("缺少「" + word + "」")
			}
		}
		if !containsAny(p, touchMarkers) {
			fail("丢了「形状靠手感可以分辨」")
		}
		if !containsAny(p, ruleMarkers) {
			fail("丢了「摸之前先定好数目」")
		}
		if !containsAny(p, []string{"最少", "至少"}) || !containsAny(p, []string{"保证", "确保"}) {
			fail("问法不再是「最少多少才能保证」")
		}
		if !clarifyAppleFirst.MatchString(p) || !clarifyPeachFirst.MatchString(p) {
			fail("两种满足方式的说明不完整")
		}
		if !strings.Contains(p, `{"answer": <整数>, "reasoning": "`) {
			fail("作答格式变了")
		}
		if strings.Contains(p, "21") || strings.Contains(p, "二十一") {
			fail("题面里出现了答案")
		}
		if strings.Contains(p, "，。") || strings.Contains(p, "。。") || strings.Contains(p, "，\n") || strings.Contains(p, "\n\n\n") {
			fail("标点或换行拼坏了")
		}
		for _, hint := range []string{"最坏", "按形状", "挑选"} {
			if strings.Contains(p, hint) {
				fail("出现了提示性的词「" + hint + "」")
			}
		}
	}
}

// 六种数量写法 × 口味顺序 × 形状先后 × 数字写法，逐一解析回（口味, 形状）→ 数量，必须与原题一致。
func TestCandyDataKeepsEveryCount(t *testing.T) {
	want := map[string][2]int{"苹果": {7, 7}, "桃子": {9, 6}, "西瓜": {8, 4}}
	for layout := 0; layout < 6; layout++ {
		for perm := 0; perm < len(candyPermutations); perm++ {
			for _, starFirst := range []bool{false, true} {
				for _, chinese := range []bool{false, true} {
					for _, star := range [][2]string{{"五角星形", "五角星"}, {"五角星", "五角星"}} {
						w := candyWording{candy: "糖", mw: "颗", suffix: "味", starFull: star[0], starShort: star[1],
							chinese: chinese, order: candyPermutations[perm], starFirst: starFirst}
						c := &chooser{rng: mrand.New(mrand.NewPCG(uint64(layout), uint64(perm))), force: map[string]int{"d": layout}}
						text := renderCandyData(c, w).text
						got := parseCandyCounts(t, text)
						for flavor, counts := range want {
							if got[flavor] != counts {
								t.Fatalf("layout %d perm %d starFirst %v chinese %v：%s 解析为 %v，应为 %v\n%s",
									layout, perm, starFirst, chinese, flavor, got[flavor], counts, text)
							}
						}
						// 口味的列举顺序要按选定的排列走
						order := flavorOrderIn(text)
						for k, i := range candyPermutations[perm] {
							if order[k] != candyFlavors[i].name {
								t.Fatalf("layout %d perm %d：口味顺序 %v 与排列不符\n%s", layout, perm, order, text)
							}
						}
					}
				}
			}
		}
	}
}

func TestRenderCandyVaries(t *testing.T) {
	rng := mrand.New(mrand.NewPCG(3, 4))
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		seen[RenderCandy(rng).Prompt] = true
	}
	if len(seen) < 298 {
		t.Fatalf("300 次只有 %d 种题面，随机性不够", len(seen))
	}
	if space := CandyVariantSpace(); space < 1e11 {
		t.Fatalf("组合数 %.0f 偏小", space)
	}
}

// 随机题面不影响判分：同样的回答，在任何题面下判分结果都一样（判分只看回答）。
func TestRandomVariantsDoNotChangeGrading(t *testing.T) {
	if CandyQuestion.Answer != 21 || CandyQuestion.Render == nil {
		t.Fatal("糖果题答案应为 21，且开启随机题面")
	}
	if got := GradeAnswer(`{"answer": 21, "reasoning": "圆 9 + 星 12"}`, CandyQuestion.Answer); got.Outcome != OutcomeCorrect {
		t.Fatalf("got %+v", got)
	}
	if ids := questionIDs(QuestionScopeAll); len(ids) != 2 || ids[0] != fixedCandyQuestionID || ids[1] != CandyQuestion.ID {
		t.Fatalf("全部口径应同时包含固定题面与随机题面，got %v", ids)
	}
}

func containsAny(text string, words []string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

var (
	shapeNumber   = regexp.MustCompile(`(圆形|五角星形?)[^0-9一二三四五六七八九十|]*?([0-9]+|[一二三四五六七八九十])`)
	flavorPattern = regexp.MustCompile(`苹果|桃子|西瓜`)
)

// parseCandyCounts 把数量段解析回 口味 → [圆形, 五角星]。表格按表头列序读，其余按「形状 … 数字」读。
func parseCandyCounts(t *testing.T, text string) map[string][2]int {
	t.Helper()
	out := map[string][2]int{}
	if strings.Contains(text, "|") {
		var columns []string
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "| "), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if cells[0] == "口味" {
				columns = cells[1:]
				continue
			}
			flavor := flavorPattern.FindString(cells[0])
			var counts [2]int
			for i, label := range columns {
				n, _ := strconv.Atoi(cells[i+1])
				counts[shapeIndex(label)] = n
			}
			out[flavor] = counts
		}
		return out
	}
	var segments []string
	switch {
	case strings.Contains(text, "；"):
		segments = strings.Split(text, "；")
	case strings.Contains(text, "\n"):
		segments = strings.Split(text, "\n")
	default:
		segments = strings.Split(text, "）、")
	}
	for _, segment := range segments {
		flavor := flavorPattern.FindString(segment)
		if flavor == "" {
			continue
		}
		var counts [2]int
		for _, m := range shapeNumber.FindAllStringSubmatch(segment, -1) {
			n, ok := parseNumber(m[2])
			if !ok {
				t.Fatalf("数字解析失败：%q", m[2])
			}
			counts[shapeIndex(m[1])] = n
		}
		out[flavor] = counts
	}
	return out
}

func shapeIndex(label string) int {
	if strings.HasPrefix(label, "圆") {
		return 0
	}
	return 1
}

func flavorOrderIn(text string) []string {
	order := []string{}
	for _, m := range flavorPattern.FindAllString(text, -1) {
		if len(order) == 0 || order[len(order)-1] != m {
			order = append(order, m)
		}
	}
	return order
}
