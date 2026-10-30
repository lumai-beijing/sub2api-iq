import type { IQBoard, IQBoardCell, IQOverview, IQStats } from '../types'

// 智商检测组件测试共用的数据。

export function stats(overrides: Partial<IQStats> = {}): IQStats {
  return {
    graded: 10, correct: 8, wrong: 2, no_answer: 0, errors: 1, swapped: 1, json_format: 10,
    accuracy_percent: 80, json_percent: 100, avg_duration_ms: 39200, ...overrides
  }
}

export function cell(overrides: Partial<IQBoardCell> = {}): IQBoardCell {
  return {
    configured: true, level: 'healthy', level_label: '智商正常', stats: stats(), insufficient: false, best: false, gap_vs_pool: null,
    strip: [
      { at: '2026-10-06T01:50:00Z', outcome: 'wrong', answer: '29', who: '#1 a', variant: 'fixed' },
      { at: '2026-10-06T01:55:00Z', outcome: 'error', who: '#2 b', variant: 'random' },
      { at: '2026-10-06T01:59:00Z', outcome: 'correct', answer: '21', who: '#3 c', variant: 'random' }
    ],
    last_at: '2026-10-06T01:59:00Z', ...overrides
  }
}

export function board(overrides: Partial<IQBoard> = {}): IQBoard {
  return {
    generated_at: '2026-10-06T02:00:00Z', window: 'recent', scope: 'all', since: null, min_samples: 5,
    columns: [
      { label: 'gpt-6-astra@medium', model: 'gpt-6-astra', reasoning_effort: 'medium' },
      { label: 'gpt-6.1-sol@medium', model: 'gpt-6.1-sol', reasoning_effort: 'medium' }
    ],
    rows: [
      {
        key: 'pool', kind: 'pool', id: 0, name: '号池整体', detail: '51 个可用 OAuth 号 · 每轮抽 6 个', enabled: true,
        level: 'danger', level_label: '降智', running: false, next_run_at: '2026-10-06T02:04:51Z', last_run_at: '2026-10-06T01:58:00Z',
        cells: [cell({ level: 'danger', level_label: '降智', stats: stats({ accuracy_percent: 25, correct: 5, graded: 20 }) }), cell()],
        total: cell({ stats: stats({ accuracy_percent: 50 }) })
      },
      {
        key: 'group:8', kind: 'group', id: 8, name: 'gpt-pro-pool-1', detail: '可用 11 / 39 个号', enabled: true,
        level: 'healthy', level_label: '智商正常', running: false, next_run_at: '2026-10-06T02:04:51Z', last_run_at: null,
        cells: [cell({ gap_vs_pool: 60, best: true, stats: stats({ accuracy_percent: 85 }) }), cell({ insufficient: true, level: 'unknown', level_label: '样本不足', stats: stats({ graded: 2, correct: 2, accuracy_percent: 100 }) })],
        total: cell()
      },
      {
        key: 'provider:3', kind: 'provider', id: 3, name: '米醋中转', detail: 'www.micuapi.ai', enabled: true,
        level: 'healthy', level_label: '智商正常', running: false, next_run_at: '2026-10-06T02:03:00Z', last_run_at: '2026-10-06T01:58:00Z',
        cells: [cell(), cell({ configured: false, stats: stats({ graded: 0, correct: 0, wrong: 0, errors: 0, accuracy_percent: null }), strip: [] })],
        total: cell()
      }
    ],
    advisories: [
      { level: 'critical', source_key: 'pool', source: '号池整体', model: 'gpt-6-astra@medium', title: '降智：多数题答错',
        detail: '最常见的错答是 29（12 次），没用上「形状靠手感能分辨」这个条件，按盲摸的最坏情况算出来的。', evidence: '最近 20 次答对 5 次（25%）' },
      { level: 'info', title: '题面已改为每次随机改写', detail: '条件、数字和答案（21）都不变。', evidence: '还有 4 处在积累样本' }
    ],
    ...overrides
  }
}

export function overview(overrides: Partial<IQOverview> = {}): IQOverview {
  return {
    generated_at: '2026-10-06T02:00:00Z',
    level: 'danger', level_label: '降智',
    gate: { open: true, required: 'lmuai-0.2.13.2', current: 'lmuai-0.2.13.2', checked_at: '2026-10-06T02:00:00Z' },
    schedule: {
      enabled: true, interval_seconds: 1200, jitter_seconds: 120, sample_mode: 'ratio', sample_percent: 10, sample_min: 2, sample_count: 5,
      config_source: 'settings', paused: false, concurrency: 2, running: false, running_targets: 0,
      last_run_at: '2026-10-06T01:58:00Z', last_finished_at: '2026-10-06T01:59:00Z', last_round_seconds: 84,
      next_run_at: '2026-10-06T02:04:51Z', next_anchor_at: '2026-10-06T02:05:00Z', skipped_slots: 0,
      candidates: 51, sample_size: 6, groups: 1, group_per_round: 2, last_round_pool: 6, last_round_top_ups: 1
    },
    question: { id: 'candy-json-v2', prompt: '在一个黑色的袋子里……', answer: 21, randomized: true, space: 2.42e12, samples: ['随机示例一', '随机示例二'] },
    models: [{ model: 'gpt-6-astra', reasoning_effort: 'medium', label: 'gpt-6-astra@medium' }],
    models_source: 'settings',
    groups: { ids: [8], per_round: 2 },
    thresholds: { recent_window: 30, min_samples: 5, healthy_percent: 80, warning_percent: 50, history_points: 180 },
    board: board(),
    ...overrides
  }
}
