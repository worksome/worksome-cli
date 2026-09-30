package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/worksome/worksome-cli/internal/buildinfo"
	"github.com/worksome/worksome-cli/internal/client"
)

// statusURL is the public Oh Dear JSON feed behind status.worksome.com.
const statusURL = "https://status.worksome.com/json"

type statusFeed struct {
	SummarizedStatus string                     `json:"summarizedStatus"`
	PinnedUpdate     *statusUpdate              `json:"pinnedUpdate"`
	Monitors         map[string][]statusMonitor `json:"monitors"`
	UpdatesPerDay    map[string][]statusUpdate  `json:"updatesPerDay"`
}

type statusMonitor struct {
	Group  string `json:"group"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Status string `json:"status"`
}

type statusUpdate struct {
	Date     string `json:"date,omitempty"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

type statusSummary struct {
	Status   string          `json:"status"`
	Pinned   *statusUpdate   `json:"pinned"`
	Monitors []statusMonitor `json:"monitors"`
	Updates  []statusUpdate  `json:"updates"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the Worksome service status from status.worksome.com",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, _ := cmd.Root().PersistentFlags().GetInt("timeout")
			if timeout < 0 {
				return fmt.Errorf("--timeout must be non-negative (got %d)", timeout)
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
				defer cancel()
			}

			summary, err := fetchStatus(ctx, statusURL)
			if err != nil {
				return err
			}

			if outputFlag, _ := cmd.Root().PersistentFlags().GetString("output"); outputFlag == "json" {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(summary)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), formatStatus(summary))
			return err
		},
	}
}

func fetchStatus(ctx context.Context, url string) (*statusSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", client.UserAgent(buildinfo.Version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching status: unexpected HTTP %d", resp.StatusCode)
	}

	var feed statusFeed
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decoding status: %w", err)
	}
	// Unknown values pass through so a new Oh Dear status doesn't break the command.
	if feed.SummarizedStatus == "" {
		return nil, fmt.Errorf("decoding status: feed has no summarizedStatus")
	}
	return summarize(feed), nil
}

// summarize flattens the feed's maps into lists: groups alphabetical, monitors in page order, days newest first.
func summarize(feed statusFeed) *statusSummary {
	s := &statusSummary{Status: feed.SummarizedStatus, Pinned: feed.PinnedUpdate, Monitors: []statusMonitor{}, Updates: []statusUpdate{}}

	groups := make([]string, 0, len(feed.Monitors))
	for g := range feed.Monitors {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		for _, m := range feed.Monitors[g] {
			m.Group = g
			s.Monitors = append(s.Monitors, m)
		}
	}

	days := make([]int64, 0, len(feed.UpdatesPerDay))
	for k := range feed.UpdatesPerDay {
		if ts, err := strconv.ParseInt(k, 10, 64); err == nil {
			days = append(days, ts)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] > days[j] })
	for _, ts := range days {
		for _, u := range feed.UpdatesPerDay[strconv.FormatInt(ts, 10)] {
			u.Date = time.Unix(ts, 0).UTC().Format("2006-01-02")
			s.Updates = append(s.Updates, u)
		}
	}
	return s
}

func formatStatus(s *statusSummary) string {
	var w strings.Builder
	fmt.Fprintf(&w, "Overall: %s\n", s.Status)
	if s.Pinned != nil {
		fmt.Fprintf(&w, "Pinned:  [%s] %s\n", s.Pinned.Severity, s.Pinned.Title)
	}

	group := ""
	for _, m := range s.Monitors {
		if m.Group != group {
			group = m.Group
			fmt.Fprintf(&w, "\n%s\n", group)
		}
		fmt.Fprintf(&w, "  %-6s %s\n", m.Status, m.Label)
	}

	fmt.Fprintln(&w, "\nRecent updates:")
	if len(s.Updates) == 0 {
		fmt.Fprintln(&w, "  none")
	}
	for _, u := range s.Updates {
		fmt.Fprintf(&w, "  %s  [%s] %s\n", u.Date, u.Severity, u.Title)
	}
	fmt.Fprintln(&w, "\nhttps://status.worksome.com")
	return w.String()
}
