package cron

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

type ParseResult struct {
	Valid       bool
	HasSecond   bool
	Fields      map[string]string
	Description string
	Error       string
}

func SplitExpressions(raw string) []string {
	lines := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func NormalizeExpressions(raw string) string {
	return strings.Join(SplitExpressions(raw), "\n")
}

func ValidateExpressions(raw string) error {
	expressions := SplitExpressions(raw)
	if len(expressions) == 0 {
		return fmt.Errorf("请至少填写一条定时规则")
	}

	for index, expression := range expressions {
		result := Parse(expression)
		if !result.Valid {
			return fmt.Errorf("第 %d 条定时规则无效: %s", index+1, result.Error)
		}
	}
	return nil
}

func Parse(expression string) ParseResult {
	expression = strings.TrimSpace(expression)
	parts := strings.Fields(expression)

	parser, hasSecond, err := parserForParts(parts)
	if err != nil {
		return ParseResult{Valid: false, Error: err.Error()}
	}

	if _, err := parser.Parse(expression); err != nil {
		return ParseResult{Valid: false, Error: err.Error()}
	}

	fields := buildFields(parts, hasSecond)
	return ParseResult{
		Valid:       true,
		HasSecond:   hasSecond,
		Fields:      fields,
		Description: describe(fields, hasSecond),
	}
}

func NextRunTimes(expression string, count int) []time.Time {
	return NextRunTimesFrom(expression, count, time.Now())
}

func NextRunTimesFrom(expression string, count int, from time.Time) []time.Time {
	if count <= 0 {
		return nil
	}

	schedule, err := ParseSchedule(expression)
	if err != nil {
		return nil
	}

	times := make([]time.Time, 0, count)
	next := from
	for i := 0; i < count; i++ {
		next = schedule.Next(next)
		if next.IsZero() {
			break
		}
		times = append(times, next)
	}
	return times
}

func NextRunTimesForExpressions(raw string, count int) []time.Time {
	return NextRunTimesForExpressionsFrom(raw, count, time.Now())
}

func NextRunTimesForExpressionsFrom(raw string, count int, from time.Time) []time.Time {
	if count <= 0 {
		return nil
	}

	expressions := SplitExpressions(raw)
	if len(expressions) == 0 {
		return nil
	}

	times := make([]time.Time, 0, len(expressions)*count)
	for _, expression := range expressions {
		times = append(times, NextRunTimesFrom(expression, count, from)...)
	}

	sort.Slice(times, func(i, j int) bool {
		return times[i].Before(times[j])
	})

	if len(times) > count {
		times = times[:count]
	}
	return times
}

// NextRunTimesForExpressionsBetween 求一组定时规则（换行分隔）在 (from, to) 内的触发时刻，合并、升序后最多 limit 个。
//
// 与「NextRunTimesForExpressionsFrom(raw, limit, from) 再丢掉 >= to 的点」结果完全一致，只是不再白算窗口外的点：
// 旧写法对每条规则固定往后推 limit 个（每天一次的任务 limit=1440 时要推到约 4 年后），任务一多 /tasks/upcoming 就慢。
// 这里每条规则只解析一次，从 from 起逐个求下次触发，碰到 to 就停；每条规则仍受 limit 约束，合并后再截到 limit，
// 所以「多条规则重复触发同一时刻会出现两次」「单条规则超过 limit 被截断」这些旧行为都保持不变。
func NextRunTimesForExpressionsBetween(raw string, from, to time.Time, limit int) []time.Time {
	if limit <= 0 || !from.Before(to) {
		return nil
	}

	times := make([]time.Time, 0)
	for _, expression := range SplitExpressions(raw) {
		schedule, err := ParseSchedule(expression)
		if err != nil {
			continue
		}
		next := from
		for i := 0; i < limit; i++ {
			next = schedule.Next(next)
			// 永不触发（如 2 月 30 日）时 robfig 返回零值；到了窗口末尾也停
			if next.IsZero() || !next.Before(to) {
				break
			}
			times = append(times, next)
		}
	}

	sort.Slice(times, func(i, j int) bool {
		return times[i].Before(times[j])
	})
	if len(times) > limit {
		times = times[:limit]
	}
	return times
}

// FirstRunSecondOfDay 算出这组定时规则「一天之内最早一次触发」的时刻，单位是距 0 点的秒数（0-86399）。
// 任务列表按「定时规则」排序时用它当排序键（issue #151）：用户要的是一条 00:00→23:59 的时间线，
// 用来看哪个时间段扎堆了哪些脚本，所以只取时:分:秒、不管落在哪一天——每周一 09:00 和每天 09:00 算同一时刻。
// 结果只取决于规则本身、与「现在几点」无关，刷新列表时顺序不会漂移（这一点和「下次运行」排序不同）。
//
// 每行 cron 各自从本地「今天 0 点」起找第一次触发，多行取最小：
//   - 起点故意用 0 点减 1 秒：robfig 的 Next 会先把起点进位到下一整秒再找，这样 00:00:00 本身也能命中。
//     直接从 0 点起找的话 0 点被排除在外，`0 0 0,12 * * *` 会被算成 12:00、`*/10 * * * * *` 会被算成 00:00:10；
//   - 第一次触发不一定在今天（周三算每周一的任务会落到下周一），但时刻相同，只取时分秒不受影响；
//   - `*/5` 这类高频规则落在 00:00，带时段的（9-22 点每 10 分钟）落在时段起点，都是它们确实会跑的时刻。
//
// 一行都算不出（空串、表达式非法、2 月 30 日这种永不触发）时返回 false，由调用方当成「没有值」。
func FirstRunSecondOfDay(raw string, now time.Time) (int, bool) {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	best, found := 0, false
	for _, expression := range SplitExpressions(raw) {
		times := NextRunTimesFrom(expression, 1, midnight.Add(-time.Second))
		if len(times) == 0 {
			continue
		}
		second := times[0].Hour()*3600 + times[0].Minute()*60 + times[0].Second()
		if !found || second < best {
			best, found = second, true
		}
	}
	return best, found
}

func parserForParts(parts []string) (robfigcron.Parser, bool, error) {
	switch len(parts) {
	case 5:
		return robfigcron.NewParser(
			robfigcron.Minute |
				robfigcron.Hour |
				robfigcron.Dom |
				robfigcron.Month |
				robfigcron.Dow |
				robfigcron.Descriptor,
		), false, nil
	case 6:
		return robfigcron.NewParser(
			robfigcron.Second |
				robfigcron.Minute |
				robfigcron.Hour |
				robfigcron.Dom |
				robfigcron.Month |
				robfigcron.Dow |
				robfigcron.Descriptor,
		), true, nil
	default:
		return robfigcron.Parser{}, false, errInvalidFieldCount
	}
}

func parseSchedule(expression string) (robfigcron.Schedule, error) {
	return ParseSchedule(expression)
}

func ParseSchedule(expression string) (robfigcron.Schedule, error) {
	expression = strings.TrimSpace(expression)
	parts := strings.Fields(expression)
	parser, _, err := parserForParts(parts)
	if err != nil {
		return nil, err
	}
	return parser.Parse(expression)
}

// errInvalidFieldCount 是段数不合法时唯一的报错出口，文案直接会显示在面板上，所以说人话并交代两件事：
//  1. 6 段和 5 段各自的段位含义。两者都合法，但少写/多写一段会让每一段的含义整体平移
//     （6 段的 `0 5 * * * *` 是每小时第 5 分钟，5 段的 `0 5 * * *` 却是每天 05:00），
//     不写清楚的话用户只会得到一句「必须是 5 段或 6 段」，仍然不知道自己错在哪；
//  2. @daily / @hourly / @every 1h / TZ= 前缀这类简写【不支持】。parserForParts 先按 strings.Fields 的
//     段数分派，这些写法（1 段、2 段、7 段）根本进不到解析器，而用户很可能会照着别处的教程去试。
//
// 刻意不在这里举「0 0 5 * * * = 每天 05:00:00」这类例子：这条文案会整块渲染成红色错误徽标，
// 徽标可用宽只有 500px 左右，句子越长红块越厚（原来的版本要压 4~6 行）。
// 举例和「少一段含义整体前移」的展开说明由输入框下方常驻的 .cron-field-hint 承担，那里本来就写了同样的内容。
var errInvalidFieldCount = &parseError{message: "定时规则必须是 5 段或 6 段，用空格分隔：" +
	"6 段是「秒 分 时 日 月 周」，5 段是「分 时 日 月 周」；" +
	"不支持 @daily、@every 1h 这类简写"}

type parseError struct {
	message string
}

func (e *parseError) Error() string {
	return e.message
}

func buildFields(parts []string, hasSecond bool) map[string]string {
	if hasSecond {
		return map[string]string{
			"second":      parts[0],
			"minute":      parts[1],
			"hour":        parts[2],
			"day":         parts[3],
			"month":       parts[4],
			"day_of_week": parts[5],
		}
	}

	return map[string]string{
		"minute":      parts[0],
		"hour":        parts[1],
		"day":         parts[2],
		"month":       parts[3],
		"day_of_week": parts[4],
	}
}

func describe(fields map[string]string, hasSecond bool) string {
	second := fields["second"]
	minute := fields["minute"]
	hour := fields["hour"]
	day := fields["day"]
	rawMonth := fields["month"]
	rawDow := fields["day_of_week"]

	// 秒段必须是固定值，下面所有「每分钟 / 每小时 / HH:MM / 每月X日」的说法才成立：
	// 秒是 `*` 或 `*/N` 时这些话都是【自信的错误】——`*/10 0 9 * * *` 每天跑 6 次，
	// 说成「每天 09:00」比含糊更坏，所以这类表达式一路落兜底。
	// 注意「每N小时」与「时段 + 星期」两条另有更严的 isZeroField(second) 守卫，不走这个变量。
	secondFixed := !hasSecond || isNumeric(second)

	// 三条 `*/N` 早退分支必须连其它段一起看，否则会给出【自信的错误】。
	// 例：`*/10 9-22 * * 1-5` 只看分段的话会被说成「每10分钟」，把「9-22 点」和「工作日」整段吃掉 ——
	// 这条表达式本来就能存、能校验、也确实只在工作日 9-22 点跑，可用户看到这句只会以为面板没认出时段和星期。
	// 兜底文案只是含糊，这种是错的，比含糊更坏。所以只有其余段确实「不限定」时才允许早退，
	// 否则一路往下走，交给下面的「时段 + 星期」分支，实在认不出再落兜底。
	//
	// 秒步进这条比较特殊：下游没有任何分支能翻译「每 N 秒」，被守卫拦下就直接落兜底。
	// 这是刻意的 —— `*/2 0 10 * * *` 在 10:00 那一分钟里跑 30 次，说成「每天 10:00」是错的。
	if hasSecond && isEvery(minute) && isEvery(hour) && isEvery(day) && isEvery(rawMonth) && isEvery(rawDow) {
		if desc, ok := describeSimpleStep(second, "秒"); ok {
			return desc
		}
	}
	if secondFixed && isEvery(hour) && isEvery(day) && isEvery(rawMonth) && isEvery(rawDow) {
		if desc, ok := describeSimpleStep(minute, "分钟"); ok {
			return desc
		}
	}
	// 时步进除了后面几段，还要求分（6 段时连秒）固定为 0：
	// 不看分的话 `0 30 */2 * * *` 会被说成「每2小时」，把「每小时第 30 分钟」这层含义吃掉。
	if isEvery(day) && isEvery(rawMonth) && isEvery(rawDow) && isZeroField(minute) && (!hasSecond || isZeroField(second)) {
		if desc, ok := describeSimpleStep(hour, "小时"); ok {
			return desc
		}
	}

	month := normalizeMonth(rawMonth)
	// 「每天 …」这几条分支要补的秒位，详见 dailySecondSuffix 的注释
	secondSuffix := dailySecondSuffix(fields, hasSecond)
	// 星期前缀：「每天」「工作日」「周末」「每周一」……；认不出（`MON`、`1-5/2` 等）时 weekOK 为 false，
	// 下面所有带星期前缀的分支都会跳过，整条描述落到兜底，详见 weekdayPrefix 的注释。
	weekPrefix, weekOK := weekdayPrefix(rawDow)

	// 「每分钟」同样要看秒和星期：`*/10 * * * * 1-5` 是「工作日每 10 秒」，说成「每分钟」
	// 既把频率说少了 6 倍，又把星期限定整个吃掉。
	// 秒这里用 isNumeric 而不是 isZeroField：固定秒（`30 * * * * *`）本来就是每分钟触发一次，
	// 说「每分钟」不算错；要挡住的是 `*/N` 和裸 `*`（`* * * * * *` 其实是每秒执行）。
	// 星期不限定时仍走裸文案：`0 * * * * *` 说成「每天 每分钟」既拗口，也会顶掉出厂预设的说法；
	// `MON`、`1-5/2` 这类 weekOK 为 false 的写法自动落兜底，与 weekdayPrefix 的契约一致。
	if secondFixed && isEvery(month) && isEvery(day) && isEvery(hour) && isEvery(minute) {
		if isEvery(rawDow) {
			return "每分钟"
		}
		if weekOK {
			return weekPrefix + " 每分钟"
		}
	}
	// 「每小时整点 / 每小时第 N 分钟」：出厂预设「每小时」`0 0 * * * *` 的 name 与 description
	// 都写着人话，描述却一直落在兜底 —— 用户在预设面板看到「每小时整点执行」，选中后规则行的徽标
	// 却退化成「自定义 cron 表达式」，同一屏里两个说法。小时段是裸 `*`，describeSimpleStep 认不出，
	// 后面的分支又都要求小时是纯数字、区间或逗号列表，于是没人接手，这里补上。
	// 秒必须固定为 0（`*/10 30 * * * *` 那一分钟里跑 6 次，不能说成「每小时第 30 分钟」）；
	// 星期的处理与上面的「每分钟」一致。
	if isEvery(month) && isEvery(day) && isEvery(hour) && isNumeric(minute) && (!hasSecond || isZeroField(second)) {
		hourly := "每小时第 " + minute + " 分钟"
		if isZeroField(minute) {
			hourly = "每小时整点"
		}
		if isEvery(rawDow) {
			return hourly
		}
		if weekOK {
			return weekPrefix + " " + hourly
		}
	}
	// 「时段 + 星期」：`0 */10 9-22 * * 1-5` 这类表达式一直能存能跑，只是以前被上面的 `*/N` 早退
	// 吃成了「每10分钟」。这里把小时段和星期段一起翻出来，说清楚「工作日 9-22 点，每 10 分钟」。
	// 前置条件收得很紧：day / month 必须都不限定；6 段时秒必须固定为 0（秒是 `*` 或 `*/N` 时
	// 「每 N 分钟」这句话就不成立了，此时不命中本分支）；小时段只认 `A-B` 与单个小时，
	// `9-22/2`、`9-11,21` 这种一律不猜，落兜底。
	if weekOK && isEvery(month) && isEvery(day) && (!hasSecond || isZeroField(second)) {
		rangeText, isRange := hourRangeText(hour)
		// 分段是 `*/N`：小时可以是区间（9-22 点）也可以是单个小时（9 点）
		if step, ok := stepValue(minute); ok {
			if isRange {
				return weekPrefix + " " + rangeText + " 点，每 " + step + " 分钟"
			}
			if isHourNumber(hour) {
				return weekPrefix + " " + hour + " 点，每 " + step + " 分钟"
			}
		}
		// 分段是固定数字：只有小时是区间时才走这里，单个小时仍交给下面的「HH:MM」分支，
		// 免得把「工作日 09:00」说成更绕的「工作日 9 点整点」。
		if isRange && isNumeric(minute) {
			if isZeroField(minute) {
				return weekPrefix + " " + rangeText + " 点整点"
			}
			return weekPrefix + " " + rangeText + " 点，每小时第 " + minute + " 分钟"
		}
	}
	if secondFixed && weekOK && isEvery(month) && isEvery(day) && hour == "0" && minute == "0" {
		return weekPrefix + " 00:00" + secondSuffix
	}
	// 小时段是 `9,21` 这种逗号分隔的数字列表时，逐个拼成「每天 09:50、21:50」。
	// 必须排在下面那条「每天 HH:MM」之前：那条要求小时是纯数字，含逗号会落空，
	// 一路走到兜底的「自定义 cron 表达式」。而 `10 50 9,21 * * *` 正是随机弹层
	// 「合并一条」形态的产物 —— 预览里写着人话、应用后规则条上却退化成兜底文案，
	// 同一屏里两个说法，比不给描述更像 bug。
	//
	// 星期一律走 weekPrefix：`0 30 9,21 * * 1-5` 只在工作日执行，以前这里用 isEvery(dow) 把它
	// 挡回兜底（含糊但不算错），现在能直接说「工作日 09:30、21:30」；`MON` 这类认不出的写法
	// 仍然落兜底，绝不说成「每天」——那是【自信的错误】，比含糊更坏。
	if secondFixed && weekOK && isEvery(month) && isEvery(day) && isNumeric(minute) {
		if hours := numericHourList(hour); len(hours) > 0 {
			items := make([]string, 0, len(hours))
			for _, item := range hours {
				items = append(items, item+":"+twoDigits(minute)+secondSuffix)
			}
			return weekPrefix + " " + strings.Join(items, "、")
		}
	}
	// 「每周 X HH:MM」原来是独立的一条分支，但它的前置条件是上面这条的子集，永远轮不到执行（死分支）。
	// 现在星期统一由 weekPrefix 承担，这里合并成一条：`0 0 9 * * 1-5` → 工作日 09:00、
	// `0 0 0 * * 1` → 每周一 00:00、`0 0 9 * * *` → 每天 09:00。
	if secondFixed && weekOK && isEvery(month) && isEvery(day) && isNumeric(hour) && isNumeric(minute) {
		return weekPrefix + " " + twoDigits(hour) + ":" + twoDigits(minute) + secondSuffix
	}
	// 这两条尾部分支判的是「日/月被限定了」，所以必须用 isEvery 而不是 `!= "*"`：
	// `?` 与 `*` 在 cron 里同义（robfig 解析器对两者一视同仁），而 Quartz 惯用写法
	// `0 0 9 ? * MON-FRI` 正是把日写成 `?`、由星期段说了算。用 `!= "*"` 的话 `?` 判真，
	// 这条会被说成「每月 ?日 09:00」—— 既把 `?` 原样吐进中文，又把「工作日」整段吃掉。
	// 上面带星期前缀的分支都要求 weekOK，`MON-FRI` 认不出时它们全部跳过，控制流恰好落到这里，
	// 于是同义的 `0 0 9 * * MON-FRI`（已正确落兜底）与 `? + MON-FRI` 会给出两种说法。
	// 换成 isEvery 后两者一致落兜底 —— 认不出就落兜底，绝不猜。
	if secondFixed && !isEvery(month) && !isEvery(day) && isNumeric(hour) && isNumeric(minute) {
		return "每年 " + month + " " + day + "日 " + twoDigits(hour) + ":" + twoDigits(minute)
	}
	if secondFixed && !isEvery(day) && isNumeric(hour) && isNumeric(minute) {
		return "每月 " + day + "日 " + twoDigits(hour) + ":" + twoDigits(minute)
	}
	return "自定义 cron 表达式"
}

// numericHourList 把 `9,21` 这种逗号分隔的纯数字小时段拆成补零后的列表（["09","21"]）。
// 不含逗号（单个小时，交给后面已有的分支处理）或任一段不是纯数字（`9,*/2`、`9-11,21` 等）
// 都返回 nil，宁可落到兜底文案也不猜。
func numericHourList(value string) []string {
	if !strings.Contains(value, ",") {
		return nil
	}
	items := strings.Split(value, ",")
	hours := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if !isNumeric(item) {
			return nil
		}
		hours = append(hours, twoDigits(item))
	}
	return hours
}

// weekdayPrefix 把星期段翻成描述开头的中文前缀。形态与 numericHourList 对称：认不出就返回 false，
// 让整条描述落到兜底「自定义 cron 表达式」，绝不猜 —— 说错了比不说更坏。
//
//	`*`、`?`          → 每天
//	`1-5`、`1,2,3,4,5` → 工作日（区间与逗号两种写法都要认）
//	`0,6`、`6,0`       → 周末（顺序不限）
//	单个数字            → 每周日 / 每周一 … 每周六
//	纯数字逗号列表       → 每周一、每周三
//	其它（`MON`、`1-5/2`、`5#2`、`L` 等）→ 认不出，返回 false
func weekdayPrefix(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if isEvery(value) {
		return "每天", true
	}
	// 工作日：`1-5` 与 `1,2,3,4,5` 是同一件事，两种写法都常见
	if value == "1-5" || value == "1,2,3,4,5" {
		return "工作日", true
	}
	if isWeekendField(value) {
		return "周末", true
	}
	if name, ok := weekdayName(value); ok {
		return "每" + name, true
	}
	// 其它纯数字逗号列表：`1,3` → 「每周一、每周三」；任一段认不出就整体作废
	if strings.Contains(value, ",") {
		items := strings.Split(value, ",")
		names := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := weekdayName(strings.TrimSpace(item))
			if !ok {
				return "", false
			}
			names = append(names, "每"+name)
		}
		return strings.Join(names, "、"), true
	}
	return "", false
}

// weekdayName 把单个星期数字翻成「周一」这种说法，直接复用 normalizeWeek 的映射表
// （cron 里 0 与 7 都表示周日，那张表已经处理了），免得两张表将来说法打架。
// 非「单个数字」的写法一律返回 false。
func weekdayName(value string) (string, bool) {
	if len(value) != 1 || !isNumeric(value) {
		return "", false
	}
	return normalizeWeek(value), true
}

// isWeekendField 判断星期段是不是「周六 + 周日」两天，顺序不限（`0,6`、`6,0`、`6,7` 都算）。
func isWeekendField(value string) bool {
	items := strings.Split(value, ",")
	if len(items) != 2 {
		return false
	}
	hasSaturday := false
	hasSunday := false
	for _, item := range items {
		switch strings.TrimSpace(item) {
		case "6":
			hasSaturday = true
		case "0", "7": // cron 里 0 与 7 都是周日
			hasSunday = true
		}
	}
	return hasSaturday && hasSunday
}

// hourRangeText 解析 `9-22` 这种小时区间，返回描述里直接用的文本（不补零，「9-22 点」比「09-22 点」顺口）。
// 只认 `A-B` 一种形态：A、B 都是 0-23 的纯数字且 A <= B。带步进的 `9-22/2`、混合的 `9-11,21`
// 一律返回 false，宁可整条落兜底也不猜。
func hourRangeText(value string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 2 {
		return "", false
	}
	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	if !isHourNumber(start) || !isHourNumber(end) {
		return "", false
	}
	startHour, _ := strconv.Atoi(start)
	endHour, _ := strconv.Atoi(end)
	if startHour > endHour {
		return "", false
	}
	return start + "-" + end, true
}

// isHourNumber 判断是不是 0-23 的纯数字小时。
func isHourNumber(value string) bool {
	if !isNumeric(value) {
		return false
	}
	hour, err := strconv.Atoi(value)
	return err == nil && hour >= 0 && hour <= 23
}

// stepValue 取出 `*/N` 里的 N；不是这个形态、或 N 不是纯数字时返回 false。
func stepValue(value string) (string, bool) {
	if !strings.HasPrefix(value, "*/") {
		return "", false
	}
	step := strings.TrimPrefix(value, "*/")
	if !isNumeric(step) {
		return "", false
	}
	return step, true
}

// isZeroField 判断某一段是不是固定的 0（"0"、"00" 都算），
// 用来守住「每N小时」「整点」这类只在分/秒为 0 时才成立的说法。
func isZeroField(value string) bool {
	return isNumeric(value) && strings.Trim(value, "0") == ""
}

// dailySecondSuffix 给「每天 HH:MM」这类描述补上 `:SS` 秒位。
//
// 只在「有秒段（6 段）且秒是非 0 的固定数字」时才补：
//   - 秒是 0 时不补，`0 50 9 * * *` 与 5 段的 `50 9 * * *` 描述保持一致，也避免让
//     出厂预设的描述凭空多出 `:00`；
//   - 秒是 `*/10`、`0-30` 这类非固定值时不补，兜底分支会处理：describe() 里所有拼「确定时刻」的
//     分支都带 secondFixed 守卫，秒不固定时压根走不到这里。
//
// 之所以要补：随机弹层的「随机到秒」会常态产出 `10 50 9 * * *` 这种非零固定秒，
// 而原来 describe() 只在 `*/N` 形态下提秒、固定秒值一律丢掉，于是弹层预览写「每天 09:50:10」、
// 应用后规则条却写「每天 09:50」，同一屏里两个说法。
func dailySecondSuffix(fields map[string]string, hasSecond bool) string {
	if !hasSecond {
		return ""
	}
	second := fields["second"]
	if !isNumeric(second) {
		return ""
	}
	// "0"、"00" 都算零秒，不补
	if strings.Trim(second, "0") == "" {
		return ""
	}
	return ":" + twoDigits(second)
}

func describeSimpleStep(field, unit string) (string, bool) {
	if strings.HasPrefix(field, "*/") {
		return "每" + strings.TrimPrefix(field, "*/") + unit, true
	}
	return "", false
}

func normalizeWeek(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"SUN", "周日",
		"MON", "周一",
		"TUE", "周二",
		"WED", "周三",
		"THU", "周四",
		"FRI", "周五",
		"SAT", "周六",
		"0", "周日",
		"1", "周一",
		"2", "周二",
		"3", "周三",
		"4", "周四",
		"5", "周五",
		"6", "周六",
		"7", "周日",
	)
	return replacer.Replace(upper)
}

func normalizeMonth(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"JAN", "1月",
		"FEB", "2月",
		"MAR", "3月",
		"APR", "4月",
		"MAY", "5月",
		"JUN", "6月",
		"JUL", "7月",
		"AUG", "8月",
		"SEP", "9月",
		"OCT", "10月",
		"NOV", "11月",
		"DEC", "12月",
	)
	return replacer.Replace(upper)
}

func isEvery(value string) bool {
	return value == "*" || value == "?"
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func twoDigits(value string) string {
	if len(value) == 1 {
		return "0" + value
	}
	return value
}

func GetTemplates() []map[string]string {
	return []map[string]string{
		{"name": "每分钟", "expression": "0 * * * * *", "description": "每分钟执行一次", "category": "高频"},
		{"name": "每5分钟", "expression": "0 */5 * * * *", "description": "每5分钟执行一次", "category": "高频"},
		{"name": "每10分钟", "expression": "0 */10 * * * *", "description": "每10分钟执行一次", "category": "高频"},
		{"name": "每15分钟", "expression": "0 */15 * * * *", "description": "每15分钟执行一次", "category": "高频"},
		{"name": "每30分钟", "expression": "0 */30 * * * *", "description": "每30分钟执行一次", "category": "常用"},
		{"name": "每小时", "expression": "0 0 * * * *", "description": "每小时整点执行", "category": "常用"},
		{"name": "每2小时", "expression": "0 0 */2 * * *", "description": "每2小时执行一次", "category": "常用"},
		{"name": "每6小时", "expression": "0 0 */6 * * *", "description": "每6小时执行一次", "category": "常用"},
		{"name": "每天0点", "expression": "0 0 0 * * *", "description": "每天凌晨0点执行", "category": "每天"},
		{"name": "每天6点", "expression": "0 0 6 * * *", "description": "每天早上6点执行", "category": "每天"},
		{"name": "每天9点", "expression": "0 0 9 * * *", "description": "每天上午9点执行", "category": "每天"},
		{"name": "每天12点", "expression": "0 0 12 * * *", "description": "每天中午12点执行", "category": "每天"},
		{"name": "每天18点", "expression": "0 0 18 * * *", "description": "每天下午6点执行", "category": "每天"},
		// 「时段 + 每N分钟」这三条预设（本条 + 下面工作日分类里的两条）的 description 必须写清区间是闭区间：
		// 用户很容易把「9-22 点」理解成 22:00 收尾，实际 22 点这一小时照常执行，最后一次是 22:50。
		// 不写明白的话，会被当成面板自作主张多跑了 5 次。
		{"name": "每天9-22点每10分钟", "expression": "0 */10 9-22 * * *", "description": "每天9点到22点之间每10分钟执行一次；9-22 是闭区间，22点这一小时照常执行，最后一次是 22:50 而不是 22:00", "category": "每天"},
		{"name": "工作日9点", "expression": "0 0 9 * * 1-5", "description": "工作日上午9点执行", "category": "工作日"},
		{"name": "工作日18点", "expression": "0 0 18 * * 1-5", "description": "工作日下午6点执行", "category": "工作日"},
		{"name": "工作日9-22点每10分钟", "expression": "0 */10 9-22 * * 1-5", "description": "工作日9点到22点之间每10分钟执行一次；9-22 是闭区间，22点这一小时照常执行，最后一次是 22:50 而不是 22:00", "category": "工作日"},
		{"name": "工作日9-18点每30分钟", "expression": "0 */30 9-18 * * 1-5", "description": "工作日9点到18点之间每30分钟执行一次；9-18 是闭区间，18点这一小时照常执行，最后一次是 18:30 而不是 18:00", "category": "工作日"},
		{"name": "周末10点", "expression": "0 0 10 * * 0,6", "description": "周末上午10点执行", "category": "周末"},
		{"name": "每周一0点", "expression": "0 0 0 * * 1", "description": "每周一凌晨0点执行", "category": "每周"},
		{"name": "每月1日0点", "expression": "0 0 0 1 * *", "description": "每月1日凌晨0点执行", "category": "每月"},
		{"name": "每月15日0点", "expression": "0 0 0 15 * *", "description": "每月15日凌晨0点执行", "category": "每月"},
		{"name": "每10秒", "expression": "*/10 * * * * *", "description": "每10秒执行一次", "category": "秒级"},
		{"name": "每30秒", "expression": "*/30 * * * * *", "description": "每30秒执行一次", "category": "秒级"},
	}
}
