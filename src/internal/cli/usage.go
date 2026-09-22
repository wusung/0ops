package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/wusung/0ops/internal/shared/backendclient"
	"github.com/wusung/0ops/internal/shared/dto"
)

// newUsageCommand wires `0ops usage`: what the team's apps were
// allocated, and for how long.
//
// This reports allocation, not consumption: what a pod reserved for the
// time it existed. That is the figure every platform bills on, and the
// only one that can be stated exactly.
func newUsageCommand() *cobra.Command {
	var (
		teamSlug  string
		baseURL   string
		token     string
		outputFmt string
		from      string
		to        string
		appSlug   string
		interval  bool
		observed  bool
	)
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show resource allocation for the team's apps",
		Long: "Show how much CPU, memory and GPU the team's apps were allocated and for how long.\n\n" +
			"Figures are allocation x lifetime, taken from pod start and stop times. " +
			"They are not a bill.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxInfo, err := resolveAppsContext(teamSlug, baseURL, token)
			if err != nil {
				return err
			}
			client := backendclient.New(ctxInfo.Host, ctxInfo.BearerToken)
			params := backendclient.UsageParams{From: from, To: to, Interval: interval, Observed: observed}

			var out dto.UsageResponse
			if strings.TrimSpace(appSlug) != "" {
				out, err = client.GetAppUsage(commandContext(cmd), ctxInfo.TeamSlug, strings.TrimSpace(appSlug), params)
			} else {
				out, err = client.GetTeamUsage(commandContext(cmd), ctxInfo.TeamSlug, params)
			}
			if err != nil {
				return err
			}
			return renderUsage(cmd, out, outputFmt)
		},
	}
	cmd.Flags().StringVar(&teamSlug, "team", "", "team slug")
	cmd.Flags().StringVar(&baseURL, "host", "", "backend host")
	cmd.Flags().StringVar(&token, "token", "", "bearer token")
	cmd.Flags().StringVar(&outputFmt, "output", envOr("OPS_OUTPUT", "table"), "output format")
	cmd.Flags().StringVar(&from, "from", "", "start date (YYYY-MM-DD, UTC); default 30 days ago")
	cmd.Flags().StringVar(&to, "to", "", "end date (YYYY-MM-DD, UTC); default today")
	cmd.Flags().StringVar(&appSlug, "app", "", "restrict to one app")
	cmd.Flags().BoolVar(&interval, "interval", false, "include per-pod allocation intervals")
	cmd.Flags().BoolVar(&observed, "observed", false, "also show measured consumption (last 30 days, where metrics are collected)")
	return cmd
}

func renderUsage(cmd *cobra.Command, out dto.UsageResponse, outputFmt string) error {
	switch strings.ToLower(outputFmt) {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "yaml":
		return yaml.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "WINDOW\t%s .. %s (UTC)\n\n", out.From, out.To)
	hasObserved := false
	for _, app := range out.Apps {
		if app.Observed != nil {
			hasObserved = true
			break
		}
	}

	if hasObserved {
		// Allocated and used are kept in separate columns on purpose:
		// one is what the app reserved, the other what it consumed. A
		// large gap means the app is oversized, not that a number is
		// wrong.
		fmt.Fprintln(w, "APP\tCPU CORE-HOURS\tMEM GiB-HOURS\tGPU HOURS\tPOD HOURS\tCPU USED avg/peak\tMEM USED avg/peak")
	} else {
		fmt.Fprintln(w, "APP\tCPU CORE-HOURS\tMEM GiB-HOURS\tGPU HOURS\tPOD HOURS")
	}
	for _, app := range out.Apps {
		fmt.Fprintf(w, "%s\t%.2f\t%.2f\t%.2f\t%.2f%s",
			app.AppSlug,
			app.Totals.CPUCoreHours, app.Totals.MemoryGiBHours,
			app.Totals.GPUHours, app.Totals.PodHours,
			estimateMarker(app.Totals))
		if hasObserved {
			fmt.Fprintf(w, "\t%s\t%s", formatObservedCPU(app.Observed), formatObservedMemory(app.Observed))
		}
		fmt.Fprintln(w)
	}
	if len(out.Apps) == 0 {
		fmt.Fprintln(w, "(no allocation recorded in this window)")
	} else {
		fmt.Fprintf(w, "TOTAL\t%.2f\t%.2f\t%.2f\t%.2f%s\n",
			out.Totals.CPUCoreHours, out.Totals.MemoryGiBHours,
			out.Totals.GPUHours, out.Totals.PodHours,
			estimateMarker(out.Totals))
	}

	if len(out.Intervals) > 0 {
		fmt.Fprintln(w, "\nPOD\tCPU\tMEMORY\tGPU\tSTARTED\tENDED")
		for _, in := range out.Intervals {
			ended := "(running)"
			if in.EndedAt != nil {
				ended = *in.EndedAt
				if in.Estimated {
					ended += " *"
				}
			}
			fmt.Fprintf(w, "%s\t%dm\t%d\t%d\t%s\t%s\n",
				in.PodName, in.CPUMillicores, in.MemoryBytes, in.GPUCount, in.StartedAt, ended)
		}
	}

	if out.Totals.EstimatedRatio > 0 {
		fmt.Fprintf(w, "\n* %.0f%% of this window ended at a time that was inferred rather than observed.\n"+
			"  Those figures are conservative: actual usage is at least this much.\n",
			out.Totals.EstimatedRatio*100)
	}
	if hasObserved {
		fmt.Fprintln(w, "\nCORE-HOURS columns are what the apps reserved; USED columns are what they consumed.\n"+
			"  A large gap means an app is sized larger than it needs. Measurements cover the last 30 days only.")
	}
	fmt.Fprintf(w, "\n%s\n", out.BillingDisclaimer)
	return w.Flush()
}

// formatObservedCPU renders measured CPU, or a dash when nothing was
// measured — which is a different statement from measuring zero.
func formatObservedCPU(o *dto.ObservedUsage) string {
	if o == nil || o.SampleCount == 0 {
		return "-"
	}
	return fmt.Sprintf("%dm/%dm", o.CPUMillicoresAvg, o.CPUMillicoresMax)
}

func formatObservedMemory(o *dto.ObservedUsage) string {
	if o == nil || o.SampleCount == 0 {
		return "-"
	}
	const mib = 1 << 20
	return fmt.Sprintf("%dMi/%dMi", o.MemoryBytesAvg/mib, o.MemoryBytesMax/mib)
}

func estimateMarker(t dto.UsageTotals) string {
	if t.EstimatedRatio > 0 {
		return " *"
	}
	return ""
}
