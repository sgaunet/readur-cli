package cli

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgaunet/readur-cli/internal/client"
)

// LabelsListJSON is the JSON shape emitted by `readur labels list --json`.
type LabelsListJSON struct {
	Labels   []LabelRow `json:"labels"`
	Total    int        `json:"total"`
	ExitCode int        `json:"exit_code"`
}

// LabelRow is the per-label record in JSON output. It mirrors the
// client.Label struct but trims zero-value timestamps to keep the
// output compact for the common case.
type LabelRow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Color         string `json:"color,omitempty"`
	Description   string `json:"description,omitempty"`
	DocumentCount int    `json:"document_count"`
	CreatedBy     string `json:"created_by,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
}

func newLabelsCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "Inspect labels known to the Readur server.",
	}
	cmd.AddCommand(newLabelsListCommand(g))
	return cmd
}

func newLabelsListCommand(g *Globals) *cobra.Command {
	var (
		sortBy string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all labels defined on the active Readur server.",
		Long: `Fetches every label from the Readur server and prints them to
stdout. Human output is a table sorted by name; JSON output carries
the full record plus total count.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pctx, err := loadProfileContext(g)
			if err != nil {
				return err
			}
			httpClient := buildHTTPClient(g, pctx)

			labels, err := httpClient.ListLabels(cmd.Context())
			if err != nil {
				return fmt.Errorf("list labels: %w", err)
			}
			sortLabels(labels, sortBy)

			if g.JSON {
				rows := make([]LabelRow, 0, len(labels))
				for _, l := range labels {
					rows = append(rows, labelToRow(l))
				}
				return g.Writer.Primary(LabelsListJSON{
					Labels:   rows,
					Total:    len(rows),
					ExitCode: 0,
				})
			}

			if len(labels) == 0 {
				return g.Writer.Primary("(no labels defined)")
			}
			return g.Writer.Primary(renderLabelsTable(labels))
		},
	}
	cmd.Flags().StringVar(&sortBy, "sort", "name", "sort by: name | count | id")
	return cmd
}

// sortLabels sorts in place by the given key. Unknown keys fall back
// to name — USAGE is not reported here because any value is at worst
// a no-op alphabetic sort.
func sortLabels(ls []client.Label, key string) {
	switch strings.ToLower(key) {
	case "count":
		sort.Slice(ls, func(i, j int) bool {
			if ls[i].DocumentCount != ls[j].DocumentCount {
				return ls[i].DocumentCount > ls[j].DocumentCount
			}
			return strings.ToLower(ls[i].Name) < strings.ToLower(ls[j].Name)
		})
	case "id":
		sort.Slice(ls, func(i, j int) bool { return ls[i].ID < ls[j].ID })
	default:
		sort.Slice(ls, func(i, j int) bool {
			return strings.ToLower(ls[i].Name) < strings.ToLower(ls[j].Name)
		})
	}
}

// renderLabelsTable produces a tab-aligned human table. Description is
// truncated to keep lines short; users wanting full output can use --json.
func renderLabelsTable(ls []client.Label) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tCOUNT\tCOLOR\tID\tDESCRIPTION")
	for _, l := range ls {
		desc := truncate(l.Description, 48)
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", l.Name, l.DocumentCount, dashIfEmpty(l.Color), l.ID, dashIfEmpty(desc))
	}
	_ = tw.Flush()
	return strings.TrimRight(b.String(), "\n")
}

func labelToRow(l client.Label) LabelRow {
	r := LabelRow{
		ID:            l.ID,
		Name:          l.Name,
		Color:         l.Color,
		Description:   l.Description,
		DocumentCount: l.DocumentCount,
		CreatedBy:     l.CreatedBy,
	}
	if !l.CreatedAt.IsZero() {
		r.CreatedAt = l.CreatedAt.UTC().Format(time.RFC3339)
	}
	return r
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit < 4 {
		return s[:limit]
	}
	return s[:limit-1] + "…"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
