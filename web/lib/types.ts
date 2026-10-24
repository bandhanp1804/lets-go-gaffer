// These mirror the JSON the Go server sends (internal/service/views.go and extras.go).
// They are written by hand, so a field renamed in Go must be renamed here.

export interface Player {
  id: number;
  name: string;
  team: string;
  position: "GKP" | "DEF" | "MID" | "FWD" | string;
  price: string;
  sell_price?: string;
  xp: number;
  form: string;
  total_points: number;
  status: string;
  news?: string;
  fixture?: string;
}

export interface MyTeam {
  manager: string;
  team_name: string;
  gameweek: number;
  source: "live" | "public" | string;
  squad_as_of: string;
  starting: Player[];
  bench: Player[];
  captain: string;
  vice_captain: string;
  bank: string;
  free_transfers_left: number;
  transfers_made_this_week: number;
  points_hit_so_far: number;
  chips_available?: string[];
  notes: string[];
}

export interface Lineup {
  gameweek: number;
  source: string;
  squad_as_of: string;
  formation: string;
  expected_points: number;
  starting: Player[];
  bench: Player[];
  captain: string;
  vice_captain: string;
  captain_lead_over_vice: number;
  close_calls: { bench_player: string; would_replace: string; expected_points_behind: number }[];
  other_formations: { formation: string; expected_points_vs_best: number }[];
  compared_with: string;
  same_xi_as_current: boolean;
  players_in?: string[];
  players_out?: string[];
  notes: string[];
}

export interface Move {
  summary: string;
  out: Player;
  in: Player;
}

export interface Suggestion {
  moves: Move[];
  transfers: number;
  points_hit: number;
  expected_gain_per_gameweek: number;
  net_points_over_horizon: number;
  net_points_vs_first_option: number;
  bank_after: string;
}

export interface Transfers {
  gameweek: number;
  source: string;
  squad_as_of: string;
  wildcard: boolean;
  free_transfers_left: number;
  bank: string;
  horizon_gameweeks: number;
  transfers_already_made: number;
  points_hit_so_far: number;
  suggestions: Suggestion[];
  notes: string[];
}

export interface Story {
  title: string;
  content: string;
  source: string;
  url: string;
  published: string;
  age: string;
  players?: string[];
  similarity: number;
}

export interface Status {
  gameweek: number;
  gameweek_name: string;
  deadline: string;
  deadline_in: string;
  last_finished_gameweek: number;
  history_through_gameweek: number;
  players: number;
  clubs: { short: string; name: string }[];
  news_articles: number;
  news_search_available: boolean;
  team_configured: boolean;
  session: { configured: boolean; state: string; expires_in?: string };
  notes: string[];
}

export interface Squad {
  gameweek: number;
  horizon_gameweeks: number;
  formation: string;
  cost: string;
  budget: string;
  expected_points: number;
  captain: string;
  vice_captain: string;
  starting: Player[];
  bench: Player[];
  notes: string[];
}

export interface Metrics {
  spearman: number;
  mae: number;
  top20: number;
}

export interface Params {
  rate_decay: number;
  minutes_decay: number;
  prior_games: number;
  fixture_slope: number;
}

export interface Backtest {
  first_gameweek: number;
  last_gameweek: number;
  models: ({ name: string } & Metrics)[];
  per_gameweek: { gameweek: number; old_spearman: number; model_spearman: number }[];
  model_wins: number;
  default_settings: Params;
  best_settings: (Params & Metrics)[];
  walk_forward: {
    gameweek: number;
    tuned_on: string;
    settings: Params;
    tuned_spearman: number;
    default_spearman: number;
    old_scorer_spearman: number;
  }[];
  walk_forward_avg_tuned: number;
  walk_forward_avg_default: number;
  walk_forward_avg_old_scorer: number;
  notes: string[];
}

export interface AskSource {
  ref: number;
  title: string;
  url: string;
  source: string;
  age: string;
}

export interface AskDone {
  session_id: string;
  text: string;
  sources: AskSource[];
  warnings: string[];
  tools: { name: string; input: string; error?: string; duration_ms: number }[];
  rounds: number;
  usage: { InputTokens: number; OutputTokens: number; CacheReadTokens: number };
}
