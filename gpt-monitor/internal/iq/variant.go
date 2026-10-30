package iq

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// 题面随机化：同一道糖果题，每次发题换一种说法。
//
// 上游（尤其是第三方中转）可能对一模一样的题做问答缓存，或者认出这道检测题后换更好的模型来答，
// 测出来的就不是它平时的水平。所以每次发题都随机改写叙述方式，但题目本身一律不变：
//   - 黑色袋子（看不见口味）、三种口味、每种都有圆形和五角星形、形状靠手感能分辨；
//   - 六个数量与口味/形状的对应关系；
//   - 摸之前要先定好摸几个；问最少摸几个才能保证手里同时有不同形状的苹果味和桃子味；
//   - 两种满足方式的说明；只输出 {"answer", "reasoning"} JSON 的作答格式；答案 21。
//
// 只改：措辞、同义词（个/颗/块、糖/糖果、味/口味）、数量的写法（行内、分条、表格、句子）、
// 口味的列举顺序、形状的先后、阿拉伯数字或汉字数字、段落排布、开头引语、作答要求的说法。
// 不加任何提示性的话（不提「最坏情况」「可以按形状挑」之类），也不删任何条件，难度不变。
// 每个槽位的第 0 项拼起来就是原题（有测试守着）。

// 历史上的固定题面。新发的题都是随机题面；统计时两者合在一起（同一道题、同一个答案），
// 也可以分开看：随机化之后正确率明显下降，说明之前的高分可能来自缓存。
const fixedCandyQuestionID = "candy-json-v1"

// Variant 是一道题的一个具体题面。
type Variant struct {
	Prompt    string
	Signature string // 每个槽位选了第几项，例如 "a1.t0.c3…"，进库便于回查
}

type candyFlavor struct {
	name        string
	round, star int
}

// 数量与口味/形状的对应关系就是题目本身，任何题面都不能改。
var candyFlavors = [3]candyFlavor{{"苹果", 7, 7}, {"桃子", 9, 6}, {"西瓜", 8, 4}}

// candyPermutations 是三种口味的列举顺序，第 0 项是原题的顺序。
var candyPermutations = [6][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}

// chooser 给每个槽位选一项并记下来。original 时全选第 0 项；force 指定某些槽位（统计组合数用）。
type chooser struct {
	rng      *rand.Rand
	original bool
	force    map[string]int
	counts   map[string]int // 每个槽位有几个选项
	sig      []string
}

func (c *chooser) pick(key string, n int) int {
	i := 0
	switch forced, ok := c.force[key]; {
	case ok:
		i = forced
	case !c.original && n > 1:
		i = c.rng.IntN(n)
	}
	if c.counts != nil {
		c.counts[key] = n
	}
	c.sig = append(c.sig, key+strconv.Itoa(i))
	return i
}

func (c *chooser) flag(key string) bool { return c.pick(key, 2) == 1 }

func (c *chooser) choose(key string, options []string) string {
	return options[c.pick(key, len(options))]
}

// candyWording 是一次渲染选定的措辞。
type candyWording struct {
	container, candy, mw, suffix, starFull, starShort, actor, event string
	chinese                                                         bool
	order                                                           [3]int
	starFirst                                                       bool
}

func (w candyWording) flavor(i int) string { return candyFlavors[i].name + w.suffix }

// count 是「圆形 7 颗」或「圆形七颗」：阿拉伯数字两边留空格，汉字数字不留。unit 为空时不带量词。
func (w candyWording) count(label string, n int, unit string) string {
	if w.chinese {
		return label + chineseNumeral(n) + unit
	}
	if unit == "" {
		return label + " " + strconv.Itoa(n)
	}
	return label + " " + strconv.Itoa(n) + " " + unit
}

type shapeCount struct {
	label string
	n     int
}

// shapes 按选定的先后顺序给出两种形状及数量。
func (w candyWording) shapes(f candyFlavor, star string) [2]shapeCount {
	round, starred := shapeCount{"圆形", f.round}, shapeCount{star, f.star}
	if w.starFirst {
		return [2]shapeCount{starred, round}
	}
	return [2]shapeCount{round, starred}
}

func chineseNumeral(n int) string {
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九", "十"}
	if n >= 0 && n < len(digits) {
		return digits[n]
	}
	return strconv.Itoa(n)
}

// part 是题面的一段。joinsComma：可以直接接在以「，」结尾的上一段后面（原题就是这样一逗到底的）；
// 否则上一段末尾的「，」改成「。」。
type part struct {
	text       string
	joinsComma bool
}

// RenderCandy 随机生成一个题面。
func RenderCandy(rng *rand.Rand) Variant { return renderCandy(&chooser{rng: rng}) }

// OriginalCandy 是原题（每个槽位都选第 0 项）。
func OriginalCandy() Variant { return renderCandy(&chooser{original: true}) }

func renderCandy(c *chooser) Variant {
	actors := [][2]string{{"参赛者", "活动"}, {"参赛者", "比赛"}, {"参与者", "活动"}, {"玩家", "游戏"}}
	actor := actors[c.pick("a", len(actors))]
	stars := [][2]string{{"五角星形", "五角星"}, {"五角星", "五角星"}}
	star := stars[c.pick("t", len(stars))]
	w := candyWording{
		container: c.choose("c", []string{"一个黑色的袋子", "一只黑色的布袋", "一个不透明的黑色袋子", "一个黑色的口袋", "一个黑布袋子"}),
		candy:     c.choose("k", []string{"糖果", "糖"}),
		mw:        c.choose("m", []string{"个", "颗", "块"}),
		suffix:    c.choose("f", []string{"味", "口味"}),
		starFull:  star[0],
		starShort: star[1],
		actor:     actor[0],
		event:     actor[1],
		chinese:   c.flag("n"),
		order:     candyPermutations[c.pick("p", len(candyPermutations))],
		starFirst: c.flag("s"),
	}
	apple, peach := w.flavor(0), w.flavor(1)
	pairA, pairB := apple, peach
	if c.flag("q") {
		pairA, pairB = peach, apple
	}

	opener := c.choose("o", []string{"", "请解答下面这道题。\n\n", "题目：\n", "请回答以下问题。\n\n", "看下面这道题：\n\n"})

	flavorList := make([]string, 0, 3)
	for _, i := range w.order {
		flavorList = append(flavorList, w.flavor(i))
	}
	setup := []string{
		"在" + w.container + "里放有三种口味的" + w.candy + "，",
		w.container + "里装着三种口味的" + w.candy + "，",
		"有" + w.container + "，里面装有" + strings.Join(flavorList, "、") + "三种口味的" + w.candy + "，",
		"某次摸糖果" + w.event + "中，" + w.container + "里装有三种不同口味的" + w.candy + "，",
		w.container + "中混装着三种口味的" + w.candy + "，",
	}[c.pick("b", 5)]

	shapePair := "圆形和" + w.starFull
	if w.starFirst {
		shapePair = w.starFull + "和圆形"
	}
	shapes := []string{
		"每种" + w.candy + "有两种不同的形状（" + shapePair + "，不同的形状靠手感可以分辨）。",
		"每种口味都有" + shapePair + "两种形状，不同形状的" + w.candy + "靠手感就能分辨出来。",
		"每种口味的" + w.candy + "都分" + shapePair + "两种形状（形状不同，用手一摸就能分辨）。",
		"每种" + w.candy + "都有" + shapePair + "两种形状，仅凭手感就可以分辨出形状。",
		"每种口味的" + w.candy + "都有两种形状：" + shapePair + "（不同的形状摸上去手感不一样，可以分辨）。",
	}[c.pick("h", 5)]

	data := renderCandyData(c, w)

	rule := []part{
		{w.actor + "需要在" + w.event + "前决定摸出的" + w.candy + "数目，", true},
		{w.actor + "必须在" + w.event + "开始前就定好要摸出多少" + w.mw + w.candy + "。", false},
		{w.actor + "要在" + w.event + "前先确定摸出" + w.candy + "的数量。", false},
		{"摸之前，" + w.actor + "要先说好一共摸出几" + w.mw + "。", false},
		{w.event + "规定：" + w.actor + "得在开始前报出要摸的" + w.candy + "数量。", false},
	}[c.pick("r", 5)]

	question := []part{
		{"那么，最少取出多少" + w.mw + w.candy + "才能保证手中同时拥有不同形状的" + pairA + "和" + pairB + "的糖？", true},
		{"问：至少要摸出多少" + w.mw + w.candy + "，才能保证手里同时有形状不同的" + pairA + "糖和" + pairB + "糖？", false},
		{"最少需要取出几" + w.mw + w.candy + "，才能确保手中一定同时有不同形状的" + pairA + "和" + pairB + w.candy + "？", false},
		{"请问：最少摸出多少" + w.mw + "，才能保证手上同时拿到形状不同的" + pairA + "和" + pairB + w.candy + "？", false},
		{"要保证手里同时有不同形状的" + pairA + "和" + pairB + w.candy + "，最少要取出多少" + w.mw + "？", false},
	}[c.pick("u", 5)]

	// 两种满足方式：圆形苹果味 + 五角星桃子味，或圆形桃子味 + 五角星苹果味（先后随机）。
	x1, y1, x2, y2 := apple, peach, peach, apple
	if c.flag("v") {
		x1, y1, x2, y2 = peach, apple, apple, peach
	}
	clarify := []string{
		"（同时手中有圆形" + x1 + "匹配" + w.starShort + y1 + w.candy + "，或者有圆形" + x2 + "匹配" + w.starShort + y2 + w.candy + "都满足要求）",
		"（手里有圆形" + x1 + "和" + w.starFull + y1 + "，或者有圆形" + x2 + "和" + w.starFull + y2 + "，都算满足要求）",
		"（即：圆形" + x1 + " + " + w.starFull + y1 + "，或者圆形" + x2 + " + " + w.starFull + y2 + "，两种组合有一种就算满足）",
		"（只要手中同时有一" + w.mw + "圆形的" + x1 + w.candy + "和一" + w.mw + w.starFull + "的" + y1 + w.candy +
			"，或者同时有一" + w.mw + "圆形的" + x2 + w.candy + "和一" + w.mw + w.starFull + "的" + y2 + w.candy + "，就算满足要求）",
		"（「圆形" + x1 + " + " + w.starFull + y1 + "」和「圆形" + x2 + " + " + w.starFull + y2 + "」这两种组合，手里有其中任意一种即可）",
	}[c.pick("e", 5)]

	reasoning := c.choose("j", []string{"<不超过 80 字的推理要点>", "<80 字以内的推理要点>", "<推理要点，不超过 80 字>", "<简要推理，80 字以内>"})
	format := `{"answer": <整数>, "reasoning": "` + reasoning + `"}`
	answer := []string{
		"请先完成推理，然后只输出一个 JSON 对象作为最终回答，不要输出 JSON 以外的任何文字，也不要用 Markdown 代码块包裹。格式：\n" + format,
		"推理完成后，最终只回复一个 JSON 对象：JSON 之外不要有任何文字，也不要放进 Markdown 代码块。格式如下：\n" + format,
		"请思考完毕后，只给出一个 JSON 对象作为最终答案，不要附带 JSON 以外的内容，也不要使用代码块。格式：\n" + format,
		"作答要求：完成推理后只输出一个 JSON 对象（不要加其他文字，不要用 ``` 包裹），格式为\n" + format,
		"最终答案只输出一个 JSON 对象，格式为 " + format + "。请先推理，再给出这个 JSON；JSON 以外不要输出任何文字，也不要用 Markdown 代码块。",
	}[c.pick("i", 5)]

	// 段落：原题是一整段；也可以按「场景 / 数量 / 规则 / 问题」分行。规则可以放在数量前面。
	breakAt := ""
	if c.flag("l") {
		breakAt = "\n"
	}
	parts := []part{{setup + shapes, false}}
	if c.flag("y") {
		parts = append(parts, part{breakAt, false}, rule, part{breakAt, false}, data)
	} else {
		parts = append(parts, part{breakAt, false}, data, part{breakAt, false}, rule)
	}
	parts = append(parts, part{breakAt, false}, question, part{clarify, true})
	separator := c.choose("w", []string{"\n\n", "\n"})

	prompt := opener + joinParts(parts) + separator + answer
	// 表格、分条和分段可能叠出多余的空行，统一收成最多一个空行。
	for strings.Contains(prompt, "\n\n\n") {
		prompt = strings.ReplaceAll(prompt, "\n\n\n", "\n\n")
	}
	return Variant{Prompt: prompt, Signature: strings.Join(c.sig, ".")}
}

// renderCandyData 渲染数量那一段：引语 + 数量。所有写法都按口味分组、每个口味给出两种形状的数量，
// 和原题的表一致，只换排版。
func renderCandyData(c *chooser, w candyWording) part {
	intro := func(options ...string) string { return options[c.pick("x", len(options))] }
	segments := make([]string, 0, 3)
	switch c.pick("d", 6) {
	case 0: // 原题：苹果味（圆形 7 / 五角星 7）、…，
		head := intro("现已知不同口味的糖和不同形状的数量统计如下表。", "已知各口味、各形状的"+w.candy+"数量如下：", "其中各种"+w.candy+"的数量分别是：")
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starShort)
			segments = append(segments, w.flavor(i)+"（"+w.count(s[0].label, s[0].n, "")+" / "+w.count(s[1].label, s[1].n, "")+"）")
		}
		return part{head + strings.Join(segments, "、") + "，", false}
	case 1: // 苹果味：圆形 7 颗，五角星形 7 颗；…。
		head := intro("各口味、各形状的"+w.candy+"数量如下：", "袋中"+w.candy+"的数量是：", "数量统计如下：")
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starFull)
			segments = append(segments, w.flavor(i)+"："+w.count(s[0].label, s[0].n, w.mw)+"，"+w.count(s[1].label, s[1].n, w.mw))
		}
		return part{head + strings.Join(segments, "；") + "。", false}
	case 2: // 分条
		head := intro("各口味、各形状的"+w.candy+"数量如下：", "袋中"+w.candy+"的数量统计如下：", "具体数量：")
		bullet := c.choose("g", []string{"- ", "• "})
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starFull)
			segments = append(segments, bullet+w.flavor(i)+"："+w.count(s[0].label, s[0].n, w.mw)+"，"+w.count(s[1].label, s[1].n, w.mw))
		}
		return part{head + "\n" + strings.Join(segments, "\n") + "\n", false}
	case 3: // Markdown 表格（表格里统一用阿拉伯数字）
		head := intro("各口味、各形状的"+w.candy+"数量统计如下表：", "数量统计如下表：", "不同口味和形状的"+w.candy+"数量见下表：")
		first := w.shapes(candyFlavors[w.order[0]], w.starFull)
		rows := []string{"| 口味 | " + first[0].label + " | " + first[1].label + " |", "| --- | --- | --- |"}
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starFull)
			rows = append(rows, "| "+w.flavor(i)+" | "+strconv.Itoa(s[0].n)+" | "+strconv.Itoa(s[1].n)+" |")
		}
		return part{head + "\n\n" + strings.Join(rows, "\n") + "\n", false}
	case 4: // 苹果味的糖有圆形 7 颗、五角星形 7 颗；…。
		head := intro("其中，", "具体来说，", "")
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starFull)
			segments = append(segments, w.flavor(i)+"的"+w.candy+"有"+w.count(s[0].label, s[0].n, w.mw)+"、"+w.count(s[1].label, s[1].n, w.mw))
		}
		return part{head + strings.Join(segments, "；") + "。", false}
	default: // 圆形的苹果味糖 7 颗、五角星形的苹果味糖 7 颗；…。
		head := intro("袋中有：", "已知袋里有：", "数量分别为：")
		for _, i := range w.order {
			s := w.shapes(candyFlavors[i], w.starFull)
			segments = append(segments, w.count(s[0].label+"的"+w.flavor(i)+"糖", s[0].n, w.mw)+"、"+w.count(s[1].label+"的"+w.flavor(i)+"糖", s[1].n, w.mw))
		}
		return part{head + strings.Join(segments, "；") + "。", false}
	}
}

// joinParts 把各段接起来：上一段以「，」结尾、而下一段另起一句（或中间要换行）时，把「，」改成「。」。
func joinParts(parts []part) string {
	out := ""
	for _, p := range parts {
		if p.text == "" {
			continue
		}
		if strings.HasSuffix(out, "，") && (!p.joinsComma || strings.HasPrefix(p.text, "\n")) {
			out = strings.TrimSuffix(out, "，") + "。"
		}
		out += p.text
	}
	return out
}

// CandyVariantSpace 是题面有多少种组合（按每个槽位的选择方式计数）。数量那一段的写法不同，
// 后面跟的槽位也不同（只有分条才有项目符号），所以按六种写法分别算再相加。
func CandyVariantSpace() float64 {
	total := 0.0
	for layout := 0; layout < 6; layout++ {
		c := &chooser{original: true, force: map[string]int{"d": layout}, counts: map[string]int{}}
		renderCandy(c)
		product := 1.0
		for key, n := range c.counts {
			if key != "d" {
				product *= float64(n)
			}
		}
		total += product
	}
	return total
}
