package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Alert is one message. User is whose inbox it is in; "" is the owners' shared inbox. PlayerID is
// 0 when it is about no one player.
type Alert struct {
	ID        int64
	User      string
	Kind      string // "watch", "briefing", "health"
	Title     string
	Body      string
	URL       string
	PlayerID  int
	CreatedAt time.Time
	Read      bool
}

// nullable turns "" into SQL NULL, for the owners' alerts.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// AddAlert stores an alert and returns its id.
func (s *Store) AddAlert(ctx context.Context, a Alert) (int64, error) {
	var player *int
	if a.PlayerID != 0 {
		player = &a.PlayerID
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO alerts (user_id, kind, title, body, url, player_id)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, nullable(a.User), a.Kind, a.Title, a.Body, a.URL, player).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("add alert: %w", err)
	}
	return id, nil
}

// ListAlerts returns the newest alerts in user's inbox ("" for the owners'), up to limit, and how
// many in that inbox are unread.
func (s *Store) ListAlerts(ctx context.Context, user string, limit int) ([]Alert, int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, COALESCE(user_id, ''), kind, title, body, url, COALESCE(player_id, 0), created_at, read_at IS NOT NULL
		FROM alerts WHERE user_id IS NOT DISTINCT FROM $1 ORDER BY created_at DESC, id DESC LIMIT $2`, nullable(user), limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.User, &a.Kind, &a.Title, &a.Body, &a.URL, &a.PlayerID, &a.CreatedAt, &a.Read); err != nil {
			return nil, 0, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list alerts: %w", err)
	}
	var unread int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE user_id IS NOT DISTINCT FROM $1 AND read_at IS NULL`, nullable(user)).Scan(&unread); err != nil {
		return nil, 0, fmt.Errorf("count unread alerts: %w", err)
	}
	return out, unread, nil
}

// MarkAlertsRead marks every alert in user's inbox up to and including id as read. Alerts that
// arrive after the page was drawn have higher ids, so they stay unread.
func (s *Store) MarkAlertsRead(ctx context.Context, user string, upTo int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE alerts SET read_at = now()
		WHERE user_id IS NOT DISTINCT FROM $1 AND id <= $2 AND read_at IS NULL`, nullable(user), upTo); err != nil {
		return fmt.Errorf("mark alerts read: %w", err)
	}
	return nil
}

// Watch adds a player to user's watchlist ("" for the owners'); watching twice is not an error.
func (s *Store) Watch(ctx context.Context, user string, playerID int) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO watchlist (user_id, player_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, user, playerID); err != nil {
		return fmt.Errorf("watch player %d: %w", playerID, err)
	}
	return nil
}

// Unwatch removes a player from user's watchlist and says how many users still watch him;
// removing one not on it is not an error.
func (s *Store) Unwatch(ctx context.Context, user string, playerID int) (int, error) {
	if _, err := s.pool.Exec(ctx, `DELETE FROM watchlist WHERE user_id = $1 AND player_id = $2`, user, playerID); err != nil {
		return 0, fmt.Errorf("unwatch player %d: %w", playerID, err)
	}
	var left int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM watchlist WHERE player_id = $1`, playerID).Scan(&left); err != nil {
		return 0, fmt.Errorf("count watchers of player %d: %w", playerID, err)
	}
	return left, nil
}

// Watchlist returns the players user watches, in the order they were added.
func (s *Store) Watchlist(ctx context.Context, user string) ([]int, error) {
	return s.ints(ctx, `SELECT player_id FROM watchlist WHERE user_id = $1 ORDER BY added_at, player_id`, user)
}

// WatchedPlayers returns every player anyone watches, once each.
func (s *Store) WatchedPlayers(ctx context.Context) ([]int, error) {
	return s.ints(ctx, `SELECT DISTINCT player_id FROM watchlist ORDER BY player_id`)
}

// Watchers returns the users who watch a player ("" for the owners).
func (s *Store) Watchers(ctx context.Context, playerID int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM watchlist WHERE player_id = $1 ORDER BY user_id`, playerID)
	if err != nil {
		return nil, fmt.Errorf("read watchers of player %d: %w", playerID, err)
	}
	defer rows.Close()
	users := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, fmt.Errorf("scan watcher: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) ints(ctx context.Context, q string, args ...any) ([]int, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("read watchlist: %w", err)
	}
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan watchlist: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EvalRun is one run of the assistant's eval cases.
type EvalRun struct {
	At       time.Time
	Model    string
	PassRate float64
	Runs     int
	Failing  []string // names of the cases with a failed run
	Tokens   int
}

// SaveEvalRun stores an eval run.
func (s *Store) SaveEvalRun(ctx context.Context, r EvalRun) error {
	failing, err := json.Marshal(nonNilStrings(r.Failing))
	if err != nil {
		return fmt.Errorf("encode failing cases: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO eval_runs (model, pass_rate, runs, failing, tokens) VALUES ($1, $2, $3, $4, $5)`,
		r.Model, r.PassRate, r.Runs, failing, r.Tokens); err != nil {
		return fmt.Errorf("save eval run: %w", err)
	}
	return nil
}

// EvalRuns returns the newest eval runs, up to limit.
func (s *Store) EvalRuns(ctx context.Context, limit int) ([]EvalRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT at, model, pass_rate, runs, failing, tokens FROM eval_runs ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("read eval runs: %w", err)
	}
	defer rows.Close()
	var out []EvalRun
	for rows.Next() {
		var r EvalRun
		var failing []byte
		if err := rows.Scan(&r.At, &r.Model, &r.PassRate, &r.Runs, &failing, &r.Tokens); err != nil {
			return nil, fmt.Errorf("scan eval run: %w", err)
		}
		if err := json.Unmarshal(failing, &r.Failing); err != nil {
			return nil, fmt.Errorf("decode failing cases: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BacktestRun is how one scoring model did on the gameweeks played so far.
type BacktestRun struct {
	At        time.Time
	ThroughGW int
	Model     string
	Spearman  float64
	MAE       float64
	Top20     float64
}

// SaveBacktestRuns stores one backtest's rows (one per model) together.
func (s *Store) SaveBacktestRuns(ctx context.Context, runs []BacktestRun) error {
	err := s.execBatch(ctx, `INSERT INTO backtest_runs (through_gw, model, spearman, mae, top20) VALUES ($1, $2, $3, $4, $5)`,
		len(runs), func(i int) []any {
			r := runs[i]
			return []any{r.ThroughGW, r.Model, r.Spearman, r.MAE, r.Top20}
		})
	if err != nil {
		return fmt.Errorf("save backtest runs: %w", err)
	}
	return nil
}

// BacktestRuns returns the newest backtest rows of one model, up to limit.
func (s *Store) BacktestRuns(ctx context.Context, model string, limit int) ([]BacktestRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT at, through_gw, model, spearman, mae, top20 FROM backtest_runs
		WHERE model = $1 ORDER BY at DESC, id DESC LIMIT $2`, model, limit)
	if err != nil {
		return nil, fmt.Errorf("read backtest runs: %w", err)
	}
	defer rows.Close()
	var out []BacktestRun
	for rows.Next() {
		var r BacktestRun
		if err := rows.Scan(&r.At, &r.ThroughGW, &r.Model, &r.Spearman, &r.MAE, &r.Top20); err != nil {
			return nil, fmt.Errorf("scan backtest run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkBriefingSent records that a gameweek's briefing went out. It reports false if it already
// had, so a retried or restarted workflow never sends the same briefing twice.
func (s *Store) MarkBriefingSent(ctx context.Context, gameweek int) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO briefings (gameweek) VALUES ($1) ON CONFLICT DO NOTHING`, gameweek)
	if err != nil {
		return false, fmt.Errorf("mark briefing for GW%d: %w", gameweek, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseBriefing undoes MarkBriefingSent, for when sending failed after the mark, so a retry sends it.
func (s *Store) ReleaseBriefing(ctx context.Context, gameweek int) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM briefings WHERE gameweek = $1`, gameweek); err != nil {
		return fmt.Errorf("release briefing for GW%d: %w", gameweek, err)
	}
	return nil
}

// PlayerState is what a watch alert compares: FPL's status letter and note.
type PlayerState struct {
	Status string
	News   string
}

// PlayerStates returns every stored player's status and note, to compare with a fresh download.
func (s *Store) PlayerStates(ctx context.Context) (map[int]PlayerState, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, status, news FROM players`)
	if err != nil {
		return nil, fmt.Errorf("read player states: %w", err)
	}
	defer rows.Close()
	out := map[int]PlayerState{}
	for rows.Next() {
		var id int
		var st PlayerState
		if err := rows.Scan(&id, &st.Status, &st.News); err != nil {
			return nil, fmt.Errorf("scan player state: %w", err)
		}
		out[id] = st
	}
	return out, rows.Err()
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
