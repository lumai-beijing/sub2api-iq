package iq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"gpt-pro-monitor/internal/model"
)

// Store 是智商检测自己的 SQLite。它是监控唯一会写的数据：
// sub2api 的库仍然只读，这里存的是探测结果和界面上热改的配置。
type Store struct {
	db *sql.DB
}

// Probe 是一次探测的完整记录。ProviderID = 0 是 sub2api 的号（AccountID 是号的 ID），
// > 0 是第三方供应商（AccountID 为 0，AccountName 记供应商名）。
type Probe struct {
	ID              int64
	ProviderID      int64
	RunID           string
	AccountID       int64
	AccountName     string
	AccountType     string  // oauth | apikey；第三方和老记录为空
	SampleGroupID   int64   // >0：号池抽完后为这个分组补测的；0 = 号池抽样、手动单号或第三方
	GroupIDs        []int64 // 检测时号所属的分组，存在 iq_probe_groups
	Model           string
	ReasoningEffort string
	QuestionID      string
	QuestionVariant string // 随机题面在各槽位的选择
	Prompt          string // 发出的题面；随机化之前的记录为空（就是原题）
	Outcome         string
	AnswerValue     string
	AnswerFormat    string // json | text | none（error 时为空）
	AnswerReasoning string // JSON 作答里的 reasoning 字段
	AnswerExcerpt   string
	UpstreamModel   string
	ModelVerdict    string
	TicketInjected  *bool
	ErrorMessage    string
	DurationMs      int64
	Trigger         string
	CreatedAt       time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS iq_probes (
	id INTEGER PRIMARY KEY,
	run_id TEXT NOT NULL,
	account_id INTEGER NOT NULL,
	account_name TEXT NOT NULL,
	model TEXT NOT NULL,
	reasoning_effort TEXT NOT NULL,
	question_id TEXT NOT NULL,
	outcome TEXT NOT NULL,
	answer_value TEXT NOT NULL DEFAULT '',
	answer_format TEXT NOT NULL DEFAULT '',
	answer_reasoning TEXT NOT NULL DEFAULT '',
	answer_excerpt TEXT NOT NULL DEFAULT '',
	upstream_model TEXT NOT NULL DEFAULT '',
	model_verdict TEXT NOT NULL DEFAULT '',
	ticket_injected INTEGER,
	error_message TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER NOT NULL DEFAULT 0,
	trigger TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_iq_account_created ON iq_probes(account_id, created_at);
CREATE INDEX IF NOT EXISTS idx_iq_created ON iq_probes(created_at);
CREATE INDEX IF NOT EXISTS idx_iq_model_created ON iq_probes(model, created_at);
CREATE TABLE IF NOT EXISTS iq_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);`

// groupSchema：检测时号属于哪些分组。分组的统计按这张表归属，号以后换了分组也不影响已有的记录。
// 删探测记录（清理过期、删供应商）时级联删掉。
const groupSchema = `
CREATE TABLE IF NOT EXISTS iq_probe_groups (
	probe_id INTEGER NOT NULL REFERENCES iq_probes(id) ON DELETE CASCADE,
	group_id INTEGER NOT NULL,
	PRIMARY KEY (group_id, probe_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_iq_probe_groups_probe ON iq_probe_groups(probe_id);`

// OpenStore 打开（必要时创建）数据库文件。WAL 让看板读和探测写互不阻塞。
func OpenStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create iq db dir: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open iq db: %w", err)
	}
	// SQLite 单写者：一个连接就够，也避免 database is locked。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init iq schema: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(providerSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init iq provider schema: %w", err)
	}
	if _, err := db.Exec(groupSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init iq group schema: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate 给老库补列。CREATE TABLE IF NOT EXISTS 不会改已有的表，新增列要在这里补。
func migrate(db *sql.DB) error {
	columns := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('iq_probes')`)
	if err != nil {
		return fmt.Errorf("inspect iq schema: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("inspect iq schema: %w", err)
		}
		columns[name] = true
	}
	rows.Close()
	for _, column := range []string{"answer_format", "answer_reasoning", "account_type", "question_variant", "prompt"} {
		if columns[column] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE iq_probes ADD COLUMN ` + column + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add iq column %s: %w", column, err)
		}
	}
	// sample_group_id：为哪个分组补测的。老数据都是号池抽样，默认 0 正好。
	if !columns["sample_group_id"] {
		if _, err := db.Exec(`ALTER TABLE iq_probes ADD COLUMN sample_group_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add iq column sample_group_id: %w", err)
		}
	}
	// provider_id：0 = 号池（sub2api 的号），>0 = 第三方供应商。老数据全是号池，默认 0 正好。
	if !columns["provider_id"] {
		if _, err := db.Exec(`ALTER TABLE iq_probes ADD COLUMN provider_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add iq column provider_id: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_iq_provider_created ON iq_probes(provider_id, created_at)`); err != nil {
		return fmt.Errorf("create iq provider index: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// InsertProbe 写一条探测记录和它的分组归属（同一个事务）。
func (s *Store) InsertProbe(ctx context.Context, p Probe) (int64, error) {
	var injected any
	if p.TicketInjected != nil {
		injected = boolToInt(*p.TicketInjected)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("insert iq probe: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO iq_probes (provider_id, run_id, account_id, account_name, account_type, sample_group_id, model, reasoning_effort,
	question_id, question_variant, prompt, outcome, answer_value, answer_format, answer_reasoning, answer_excerpt,
	upstream_model, model_verdict, ticket_injected, error_message, duration_ms, trigger, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProviderID, p.RunID, p.AccountID, p.AccountName, p.AccountType, p.SampleGroupID, p.Model, p.ReasoningEffort,
		p.QuestionID, p.QuestionVariant, p.Prompt, p.Outcome, p.AnswerValue, p.AnswerFormat, p.AnswerReasoning, p.AnswerExcerpt,
		p.UpstreamModel, p.ModelVerdict, injected, p.ErrorMessage, p.DurationMs, p.Trigger, p.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("insert iq probe: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, groupID := range uniqueIDs(p.GroupIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO iq_probe_groups (probe_id, group_id) VALUES (?, ?)`, id, groupID); err != nil {
			return 0, fmt.Errorf("insert iq probe group: %w", err)
		}
	}
	return id, tx.Commit()
}

func uniqueIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

const probeColumns = `p.id, p.provider_id, p.run_id, p.account_id, p.account_name, p.account_type, p.sample_group_id,
	p.model, p.reasoning_effort, p.question_id, p.question_variant, p.prompt, p.outcome,
	p.answer_value, p.answer_format, p.answer_reasoning, p.answer_excerpt, p.upstream_model, p.model_verdict, p.ticket_injected,
	p.error_message, p.duration_ms, p.trigger, p.created_at`

func scanProbe(row interface{ Scan(...any) error }) (Probe, error) {
	var (
		p        Probe
		injected sql.NullInt64
		created  int64
	)
	if err := row.Scan(&p.ID, &p.ProviderID, &p.RunID, &p.AccountID, &p.AccountName, &p.AccountType, &p.SampleGroupID,
		&p.Model, &p.ReasoningEffort, &p.QuestionID, &p.QuestionVariant, &p.Prompt, &p.Outcome,
		&p.AnswerValue, &p.AnswerFormat, &p.AnswerReasoning, &p.AnswerExcerpt, &p.UpstreamModel, &p.ModelVerdict, &injected,
		&p.ErrorMessage, &p.DurationMs, &p.Trigger, &created); err != nil {
		return Probe{}, err
	}
	if injected.Valid {
		value := injected.Int64 == 1
		p.TicketInjected = &value
	}
	p.CreatedAt = time.Unix(created, 0).UTC()
	return p, nil
}

// Source 选出一类检测记录（来源）：
//   - pool：号池整体，按比例抽样测的 OAuth 号。不含为分组补测的（会偏向被选中的分组）和 API Key 号；
//   - accounts：sub2api 的全部号（账号排行、单号详情）；
//   - group：检测时号属于这个分组；
//   - provider：某个第三方供应商。
//
// 来源之间的记录永远不会混在一起：第三方只看 provider_id，号池那几类只看 provider_id = 0。
type Source struct {
	Kind string
	ID   int64
}

var (
	PoolSource     = Source{Kind: model.IQSourcePool}
	AccountsSource = Source{Kind: model.IQSourceAccounts}
)

func GroupSource(id int64) Source    { return Source{Kind: model.IQSourceGroup, ID: id} }
func ProviderSource(id int64) Source { return Source{Kind: model.IQSourceProvider, ID: id} }

// Key 是看板上用的来源标识：pool / accounts / group:6 / provider:2。
func (src Source) Key() string {
	switch src.Kind {
	case model.IQSourceGroup, model.IQSourceProvider:
		return src.Kind + ":" + strconv.FormatInt(src.ID, 10)
	case model.IQSourcePool:
		return model.IQSourcePool
	default:
		return model.IQSourceAccounts
	}
}

// ParseSource 解析来源标识；空串按 accounts。
func ParseSource(key string) (Source, error) {
	key = strings.TrimSpace(key)
	switch key {
	case "", model.IQSourceAccounts:
		return AccountsSource, nil
	case model.IQSourcePool:
		return PoolSource, nil
	}
	kind, rawID, ok := strings.Cut(key, ":")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 || (kind != model.IQSourceGroup && kind != model.IQSourceProvider) {
		return Source{}, fmt.Errorf("来源无效: %q", key)
	}
	return Source{Kind: kind, ID: id}, nil
}

// where 返回这个来源对 iq_probes p 的过滤条件。
func (src Source) where() (string, []any) {
	switch src.Kind {
	case model.IQSourcePool:
		return "p.provider_id = 0 AND p.sample_group_id = 0 AND p.account_type <> 'apikey'", nil
	case model.IQSourceGroup:
		return "p.provider_id = 0 AND EXISTS (SELECT 1 FROM iq_probe_groups g WHERE g.group_id = ? AND g.probe_id = p.id)", []any{src.ID}
	case model.IQSourceProvider:
		return "p.provider_id = ?", []any{src.ID}
	default:
		return "p.provider_id = 0", nil
	}
}

// ProbeFilter 选出一段探测记录。零值字段表示不过滤；Effort 只在 Model 非空时生效。
type ProbeFilter struct {
	Source      Source
	AccountID   int64
	Model       string
	Effort      *string
	QuestionIDs []string
	Outcome     string
	Since       time.Time
	Limit       int
}

// RecentProbes 按时间倒序返回最近的探测。
func (s *Store) RecentProbes(ctx context.Context, filter ProbeFilter) ([]Probe, error) {
	sourceWhere, args := filter.Source.where()
	where := []string{sourceWhere}
	if filter.AccountID > 0 {
		where = append(where, "p.account_id = ?")
		args = append(args, filter.AccountID)
	}
	if filter.Model != "" {
		where = append(where, "p.model = ?")
		args = append(args, filter.Model)
		if filter.Effort != nil {
			where = append(where, "p.reasoning_effort = ?")
			args = append(args, *filter.Effort)
		}
	}
	if clause, values := inClause("p.question_id", filter.QuestionIDs); clause != "" {
		where = append(where, clause)
		args = append(args, values...)
	}
	if filter.Outcome != "" {
		where = append(where, "p.outcome = ?")
		args = append(args, filter.Outcome)
	}
	if !filter.Since.IsZero() {
		where = append(where, "p.created_at >= ?")
		args = append(args, filter.Since.Unix())
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 180
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT `+probeColumns+` FROM iq_probes p WHERE `+strings.Join(where, " AND ")+
		` ORDER BY p.created_at DESC, p.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("query iq probes: %w", err)
	}
	defer rows.Close()
	probes := make([]Probe, 0, limit)
	for rows.Next() {
		p, err := scanProbe(rows)
		if err != nil {
			return nil, fmt.Errorf("scan iq probe: %w", err)
		}
		probes = append(probes, p)
	}
	return probes, rows.Err()
}

// LoadGroups 给一批探测补上检测时的分组归属（采样详情要展示）。
func (s *Store) LoadGroups(ctx context.Context, probes []Probe) error {
	if len(probes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(probes))
	args := make([]any, 0, len(probes))
	index := make(map[int64]int, len(probes))
	for i, p := range probes {
		ids = append(ids, "?")
		args = append(args, p.ID)
		index[p.ID] = i
	}
	rows, err := s.db.QueryContext(ctx, `SELECT probe_id, group_id FROM iq_probe_groups WHERE probe_id IN (`+strings.Join(ids, ",")+`) ORDER BY group_id`, args...)
	if err != nil {
		return fmt.Errorf("query iq probe groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var probeID, groupID int64
		if err := rows.Scan(&probeID, &groupID); err != nil {
			return err
		}
		if i, ok := index[probeID]; ok {
			probes[i].GroupIDs = append(probes[i].GroupIDs, groupID)
		}
	}
	return rows.Err()
}

func inClause(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "", nil
	}
	marks := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		marks[i], args[i] = "?", v
	}
	return column + " IN (" + strings.Join(marks, ",") + ")", args
}

// Tally 是一组探测的计数。Graded = 答对 + 答错 + 无答案，error 单列不进分母。
type Tally struct {
	Correct     int
	Wrong       int
	NoAnswer    int
	Errors      int
	Swapped     int // 上游换成了别的系列或更低版本（降智判据与看门狗同一套）
	JSONFormat  int // 按要求只给了 JSON
	DurationSum int64
	DurationN   int
	LastAt      time.Time
}

func (t Tally) Graded() int { return t.Correct + t.Wrong + t.NoAnswer }

// AccountTally 是一个号在一个窗口内的计数，按（模型, 推理强度）拆开。
type AccountTally struct {
	AccountID       int64
	AccountName     string
	AccountType     string
	Model           string
	ReasoningEffort string
	Tally
}

// tallyColumns 是聚合一组探测的计数列，与 scanTally 的顺序一致。
const tallyColumns = `sum(p.outcome = 'correct'), sum(p.outcome = 'wrong'), sum(p.outcome = 'no_answer'), sum(p.outcome = 'error'),
	sum(p.model_verdict = 'degraded'), sum(p.answer_format = 'json'),
	COALESCE(sum(CASE WHEN p.outcome <> 'error' THEN p.duration_ms END), 0),
	sum(p.outcome <> 'error'),
	max(p.created_at)`

func tallyDest(t *Tally, last *int64) []any {
	return []any{&t.Correct, &t.Wrong, &t.NoAnswer, &t.Errors, &t.Swapped, &t.JSONFormat, &t.DurationSum, &t.DurationN, last}
}

// TallyByAccount 聚合 sub2api 每个号、每个（模型, 推理强度）在 since 之后的结果。since 为零表示全部历史。
// questionIDs 非空时只统计这些题；groupID > 0 时只统计检测时属于这个分组的记录。
func (s *Store) TallyByAccount(ctx context.Context, since time.Time, questionIDs []string, groupID int64) ([]AccountTally, error) {
	source := AccountsSource
	if groupID > 0 {
		source = GroupSource(groupID)
	}
	sourceWhere, args := source.where()
	where := []string{sourceWhere, "p.created_at >= ?"}
	args = append(args, since.Unix())
	if clause, values := inClause("p.question_id", questionIDs); clause != "" {
		where = append(where, clause)
		args = append(args, values...)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT p.account_id,
	(SELECT p2.account_name FROM iq_probes p2 WHERE p2.provider_id = 0 AND p2.account_id = p.account_id ORDER BY p2.created_at DESC, p2.id DESC LIMIT 1),
	(SELECT p2.account_type FROM iq_probes p2 WHERE p2.provider_id = 0 AND p2.account_id = p.account_id ORDER BY p2.created_at DESC, p2.id DESC LIMIT 1),
	p.model, p.reasoning_effort, `+tallyColumns+`
FROM iq_probes p
WHERE `+strings.Join(where, " AND ")+`
GROUP BY p.account_id, p.model, p.reasoning_effort`, args...)
	if err != nil {
		return nil, fmt.Errorf("tally iq probes: %w", err)
	}
	defer rows.Close()
	out := []AccountTally{}
	for rows.Next() {
		var (
			item AccountTally
			last int64
		)
		dest := append([]any{&item.AccountID, &item.AccountName, &item.AccountType, &item.Model, &item.ReasoningEffort}, tallyDest(&item.Tally, &last)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan iq tally: %w", err)
		}
		item.LastAt = time.Unix(last, 0).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

// SourceTally 是一个来源在一个（模型, 推理强度, 题目）上的计数。按题目拆开，调用方可以合并，
// 也可以拿固定题面和随机题面分开比。
type SourceTally struct {
	Source          Source
	Model           string
	ReasoningEffort string
	QuestionID      string
	Tally
}

// TallySources 一次把号池整体、每个分组、每个供应商在 since 之后的结果都聚合出来（三条查询）。
func (s *Store) TallySources(ctx context.Context, since time.Time, questionIDs []string) ([]SourceTally, error) {
	clause, values := inClause("p.question_id", questionIDs)
	filter := "p.created_at >= ?"
	if clause != "" {
		filter += " AND " + clause
	}
	args := append([]any{since.Unix()}, values...)
	queries := []struct {
		kind  string
		query string
	}{
		{model.IQSourcePool, `SELECT 0, p.model, p.reasoning_effort, p.question_id, ` + tallyColumns + `
FROM iq_probes p WHERE p.provider_id = 0 AND p.sample_group_id = 0 AND p.account_type <> 'apikey' AND ` + filter + `
GROUP BY p.model, p.reasoning_effort, p.question_id`},
		{model.IQSourceProvider, `SELECT p.provider_id, p.model, p.reasoning_effort, p.question_id, ` + tallyColumns + `
FROM iq_probes p WHERE p.provider_id > 0 AND ` + filter + `
GROUP BY p.provider_id, p.model, p.reasoning_effort, p.question_id`},
		{model.IQSourceGroup, `SELECT g.group_id, p.model, p.reasoning_effort, p.question_id, ` + tallyColumns + `
FROM iq_probe_groups g JOIN iq_probes p ON p.id = g.probe_id WHERE p.provider_id = 0 AND ` + filter + `
GROUP BY g.group_id, p.model, p.reasoning_effort, p.question_id`},
	}
	out := []SourceTally{}
	for _, q := range queries {
		rows, err := s.db.QueryContext(ctx, q.query, args...)
		if err != nil {
			return nil, fmt.Errorf("tally iq sources: %w", err)
		}
		for rows.Next() {
			var (
				item SourceTally
				id   int64
				last int64
			)
			dest := append([]any{&id, &item.Model, &item.ReasoningEffort, &item.QuestionID}, tallyDest(&item.Tally, &last)...)
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan iq source tally: %w", err)
			}
			item.Source = Source{Kind: q.kind, ID: id}
			item.LastAt = time.Unix(last, 0).UTC()
			out = append(out, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LastProbedAt 返回号池里每个号最近一次被测的时间，抽样时让久未测的号优先。
func (s *Store) LastProbedAt(ctx context.Context) (map[int64]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT account_id, max(created_at) FROM iq_probes WHERE provider_id = 0 GROUP BY account_id`)
	if err != nil {
		return nil, fmt.Errorf("query iq last probed: %w", err)
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var (
			id   int64
			last int64
		)
		if err := rows.Scan(&id, &last); err != nil {
			return nil, err
		}
		out[id] = time.Unix(last, 0).UTC()
	}
	return out, rows.Err()
}

// Prune 删掉 before 之前的探测，返回删了几行。
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM iq_probes WHERE created_at < ?`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune iq probes: %w", err)
	}
	return result.RowsAffected()
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM iq_settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get iq setting %s: %w", key, err)
	}
	return value, true, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO iq_settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set iq setting %s: %w", key, err)
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
