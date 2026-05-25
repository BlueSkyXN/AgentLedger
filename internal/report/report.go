package report

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

func Generate(conn *sql.DB, reportType, since, until, groupBy string, asJSON bool) error {
	switch reportType {
	case "daily":
		return generateDaily(conn, since, until, asJSON)
	case "weekly":
		return generateWeekly(conn, since, until, asJSON)
	case "monthly":
		return generateMonthly(conn, since, until, groupBy, asJSON)
	case "models":
		return generateModels(conn, since, until, asJSON)
	case "channels":
		return generateChannels(conn, since, until, asJSON)
	case "devices":
		return generateDevices(conn, asJSON)
	case "sessions":
		return generateSessions(conn, since, until, asJSON)
	default:
		return fmt.Errorf("unknown report type: %s", reportType)
	}
}

type ReportRow struct {
	Label        string  `json:"label"`
	Events       int64   `json:"events"`
	TotalTokens  int64   `json:"total_tokens"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

func generateDaily(conn *sql.DB, since, until string, asJSON bool) error {
	query := `SELECT 
        date(timestamp_ms/1000, 'unixepoch') as day,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY day ORDER BY day DESC"

	return executeReport(conn, query, args, asJSON, "Date")
}

func generateWeekly(conn *sql.DB, since, until string, asJSON bool) error {
	query := `SELECT 
        strftime('%Y-W%W', timestamp_ms/1000, 'unixepoch') as week,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY week ORDER BY week DESC"

	return executeReport(conn, query, args, asJSON, "Week")
}

func generateMonthly(conn *sql.DB, since, until, groupBy string, asJSON bool) error {
	labelExpr := "strftime('%Y-%m', timestamp_ms/1000, 'unixepoch')"
	switch groupBy {
	case "agent":
		labelExpr = "agent || ' / ' || strftime('%Y-%m', timestamp_ms/1000, 'unixepoch')"
	case "model":
		labelExpr = "model_normalized || ' / ' || strftime('%Y-%m', timestamp_ms/1000, 'unixepoch')"
	case "provider":
		labelExpr = "model_provider || ' / ' || strftime('%Y-%m', timestamp_ms/1000, 'unixepoch')"
	case "":
		// default grouping by month only
	default:
		return fmt.Errorf("unsupported --by value %q, allowed: agent, model, provider", groupBy)
	}

	query := fmt.Sprintf(`SELECT 
        %s as label,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`, labelExpr)

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY label ORDER BY label DESC"

	return executeReport(conn, query, args, asJSON, "Month")
}

func generateModels(conn *sql.DB, since, until string, asJSON bool) error {
	query := `SELECT 
        COALESCE(model_normalized, model_raw, 'unknown') as model,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY model ORDER BY total_tokens DESC"

	return executeReport(conn, query, args, asJSON, "Model")
}

func generateChannels(conn *sql.DB, since, until string, asJSON bool) error {
	query := `SELECT 
        COALESCE(source_channel, 'unknown') as channel,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY channel ORDER BY total_tokens DESC"

	return executeReport(conn, query, args, asJSON, "Channel")
}

func generateDevices(conn *sql.DB, asJSON bool) error {
	query := `SELECT 
        d.device_name || ' (' || d.device_id || ')' as device,
        COUNT(*) as events,
        SUM(e.total_tokens) as total_tokens,
        SUM(e.input_tokens) as input_tokens,
        SUM(e.output_tokens) as output_tokens,
        SUM(e.cost_usd) as cost_usd
    FROM usage_events e
    JOIN devices d ON e.origin_device_id = d.device_id
    GROUP BY e.origin_device_id
    ORDER BY total_tokens DESC`

	return executeReport(conn, query, nil, asJSON, "Device")
}

func generateSessions(conn *sql.DB, since, until string, asJSON bool) error {
	query := `SELECT 
        COALESCE(session_id, 'no-session') as session,
        COUNT(*) as events,
        SUM(total_tokens) as total_tokens,
        SUM(input_tokens) as input_tokens,
        SUM(output_tokens) as output_tokens,
        SUM(cost_usd) as cost_usd
    FROM usage_events
    WHERE 1=1`

	var args []interface{}
	if since != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') >= ?"
		args = append(args, since)
	}
	if until != "" {
		query += " AND date(timestamp_ms/1000, 'unixepoch') <= ?"
		args = append(args, until)
	}
	query += " GROUP BY session_id ORDER BY cost_usd DESC LIMIT 50"

	return executeReport(conn, query, args, asJSON, "Session")
}

func executeReport(conn *sql.DB, query string, args []interface{}, asJSON bool, labelHeader string) error {
	rows, err := conn.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var results []ReportRow
	for rows.Next() {
		var r ReportRow
		var totalTokens, inputTokens, outputTokens sql.NullInt64
		var costUSD sql.NullFloat64
		if err := rows.Scan(&r.Label, &r.Events, &totalTokens, &inputTokens, &outputTokens, &costUSD); err != nil {
			return err
		}
		if totalTokens.Valid {
			r.TotalTokens = totalTokens.Int64
		}
		if inputTokens.Valid {
			r.InputTokens = inputTokens.Int64
		}
		if outputTokens.Valid {
			r.OutputTokens = outputTokens.Int64
		}
		if costUSD.Valid {
			r.CostUSD = costUSD.Float64
		}
		results = append(results, r)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	if len(results) == 0 {
		fmt.Println("No data found for the specified period.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\tEvents\tTokens\tInput\tOutput\tCost(USD)\n", labelHeader)
	fmt.Fprintf(w, "---\t---\t---\t---\t---\t---\n")
	for _, r := range results {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t$%.4f\n",
			truncate(r.Label, 40), r.Events, r.TotalTokens, r.InputTokens, r.OutputTokens, r.CostUSD)
	}
	_ = w.Flush()

	var totalEvents int64
	var totalTokens int64
	var totalCost float64
	for _, r := range results {
		totalEvents += r.Events
		totalTokens += r.TotalTokens
		totalCost += r.CostUSD
	}
	fmt.Printf("\nTotal: %d events, %d tokens, $%.4f\n", totalEvents, totalTokens, totalCost)

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func FormatTimestamp(ms int64) string {
	if ms == 0 {
		return "N/A"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}
