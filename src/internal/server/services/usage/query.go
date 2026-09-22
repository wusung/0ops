package usage

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/wusung/0ops/internal/server/db"
	"github.com/wusung/0ops/internal/shared/dto"
)

// IntervalRetentionDays bounds how far back raw interval detail can be
// asked for; beyond it only daily totals survive.
const IntervalRetentionDays = 395 // 13 months

// QueryStore is the read surface the usage API needs.
type QueryStore interface {
	ListDailyRollups(ctx context.Context, teamID, appID string, fromDay, toDay time.Time) ([]db.DailyRollup, error)
	ListAllocationIntervalsOverlapping(ctx context.Context, teamID, appID string, from, to time.Time) ([]db.AllocationInterval, error)
	ListAppSlugsByTeam(ctx context.Context, teamID string) (map[string]string, error)
	SummarizeObservedUsage(ctx context.Context, teamID, appID string, from, to time.Time) ([]db.ObservedUsage, error)
}

// QueryService answers usage questions for one team at a time.
type QueryService struct {
	store QueryStore
	now   func() time.Time
}

// NewQueryService wires the read path. now defaults to time.Now().UTC().
func NewQueryService(store QueryStore, now func() time.Time) *QueryService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &QueryService{store: store, now: now}
}

// Query returns usage for [fromDay, toDay] inclusive, in UTC days.
// appID is optional ("" = every app in the team).
//
// Closed days are read from the permanent rollups; the current day is
// integrated live from intervals through the same Integrate function the
// rollup uses, so the two can never disagree (spec § 14 rule #6).
func (s *QueryService) Query(ctx context.Context, teamID, appID string, fromDay, toDay time.Time, withIntervals bool) (dto.UsageResponse, error) {
	return s.query(ctx, teamID, appID, fromDay, toDay, withIntervals, false)
}

// QueryWithObserved additionally attaches what each app actually
// consumed, where that was measured. The two never merge: allocation
// answers "what was reserved", observation answers "what was used".
func (s *QueryService) QueryWithObserved(ctx context.Context, teamID, appID string, fromDay, toDay time.Time, withIntervals bool) (dto.UsageResponse, error) {
	return s.query(ctx, teamID, appID, fromDay, toDay, withIntervals, true)
}

func (s *QueryService) query(ctx context.Context, teamID, appID string, fromDay, toDay time.Time, withIntervals, withObserved bool) (dto.UsageResponse, error) {
	fromDay = utcDay(fromDay)
	toDay = utcDay(toDay)
	today := utcDay(s.now().UTC())

	rollupTo := toDay
	if !rollupTo.Before(today) {
		rollupTo = today.AddDate(0, 0, -1)
	}

	perApp := map[string]map[string]dto.UsageDay{}
	// Closed days that have a rollup row. Anything in the window without
	// one is computed from intervals below rather than reported as zero.
	settled := map[string]bool{}

	if !rollupTo.Before(fromDay) {
		rollups, err := s.store.ListDailyRollups(ctx, teamID, appID, fromDay, rollupTo)
		if err != nil {
			return dto.UsageResponse{}, fmt.Errorf("list rollups: %w", err)
		}
		for _, ru := range rollups {
			day := utcDay(ru.Day)
			settled[ru.AppID+"/"+day.Format(time.DateOnly)] = true
			addDay(perApp, ru.AppID, dto.UsageDay{
				Day:    day.Format(time.DateOnly),
				Source: dto.UsageSourceRollup,
				Totals: totalsFrom(Totals{
					CPUMillicoreSeconds: ru.CPUMillicoreSeconds,
					MemoryByteSeconds:   ru.MemoryByteSeconds,
					GPUCountSeconds:     ru.GPUCountSeconds,
					PodSeconds:          ru.PodSeconds,
					EstimatedSeconds:    ru.EstimatedSeconds,
					IntervalCount:       ru.IntervalCount,
				}),
			})
		}
	}

	// Everything the rollups did not cover: today, which no rollup ever
	// covers, plus any closed day the hourly rollup pass has not reached
	// yet. Reporting those as zero would be a different claim from "not
	// settled yet" — and the intervals are right there.
	liveFrom := fromDay
	if !rollupTo.Before(fromDay) {
		liveFrom = fromDay
	}
	liveTo := toDay
	if !liveTo.Before(today) {
		liveTo = today
	}
	if !liveTo.Before(liveFrom) {
		windowStart, _ := DayBounds(liveFrom)
		windowEnd := s.now().UTC()
		if _, dayEnd := DayBounds(liveTo); dayEnd.Before(windowEnd) {
			windowEnd = dayEnd
		}
		intervals, err := s.store.ListAllocationIntervalsOverlapping(ctx, teamID, appID, windowStart, windowEnd)
		if err != nil {
			return dto.UsageResponse{}, fmt.Errorf("list intervals: %w", err)
		}
		byApp := map[string][]db.AllocationInterval{}
		for _, in := range intervals {
			byApp[in.AppID] = append(byApp[in.AppID], in)
		}
		for day := liveFrom; !day.After(liveTo); day = day.AddDate(0, 0, 1) {
			dayKey := day.Format(time.DateOnly)
			dayFrom, dayTo := DayBounds(day)
			if dayTo.After(windowEnd) {
				dayTo = windowEnd
			}
			for id, list := range byApp {
				if settled[id+"/"+dayKey] {
					continue
				}
				totals := Integrate(list, dayFrom, dayTo)
				if totals.PodSeconds == 0 {
					continue
				}
				addDay(perApp, id, dto.UsageDay{
					Day:    dayKey,
					Source: dto.UsageSourceLive,
					Totals: totalsFrom(totals),
				})
			}
		}
	}

	slugs, err := s.store.ListAppSlugsByTeam(ctx, teamID)
	if err != nil {
		return dto.UsageResponse{}, fmt.Errorf("list app slugs: %w", err)
	}

	resp := dto.UsageResponse{
		From:              fromDay.Format(time.DateOnly),
		To:                toDay.Format(time.DateOnly),
		BillingDisclaimer: dto.UsageBillingDisclaimer,
	}
	grand := Totals{MemoryByteSeconds: big.NewInt(0)}

	for id, days := range perApp {
		app := dto.AppUsage{AppID: id, AppSlug: slugs[id]}
		appTotals := Totals{MemoryByteSeconds: big.NewInt(0)}
		for _, d := range days {
			app.Days = append(app.Days, d)
			accumulate(&appTotals, d.Totals)
			accumulate(&grand, d.Totals)
		}
		sort.Slice(app.Days, func(i, j int) bool { return app.Days[i].Day < app.Days[j].Day })
		app.Totals = totalsFrom(appTotals)
		resp.Apps = append(resp.Apps, app)
	}
	sort.Slice(resp.Apps, func(i, j int) bool { return resp.Apps[i].AppSlug < resp.Apps[j].AppSlug })
	resp.Totals = totalsFrom(grand)

	if withObserved {
		if err := s.attachObserved(ctx, &resp, teamID, appID, fromDay, toDay); err != nil {
			return dto.UsageResponse{}, err
		}
	}

	if withIntervals {
		from, _ := DayBounds(fromDay)
		_, to := DayBounds(toDay)
		intervals, err := s.store.ListAllocationIntervalsOverlapping(ctx, teamID, appID, from, to)
		if err != nil {
			return dto.UsageResponse{}, fmt.Errorf("list intervals: %w", err)
		}
		for _, in := range intervals {
			resp.Intervals = append(resp.Intervals, intervalDTO(in))
		}
	}

	return resp, nil
}

// attachObserved folds measured consumption onto the response without
// touching any allocation figure.
func (s *QueryService) attachObserved(ctx context.Context, resp *dto.UsageResponse, teamID, appID string, fromDay, toDay time.Time) error {
	from, _ := DayBounds(fromDay)
	_, to := DayBounds(toDay)
	rows, err := s.store.SummarizeObservedUsage(ctx, teamID, appID, from, to)
	if err != nil {
		return fmt.Errorf("summarize observed usage: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	byAppDay := make(map[string]map[string]db.ObservedUsage, len(rows))
	perApp := map[string][]db.ObservedUsage{}
	for _, row := range rows {
		day := utcDay(row.Day).Format(time.DateOnly)
		if byAppDay[row.AppID] == nil {
			byAppDay[row.AppID] = map[string]db.ObservedUsage{}
		}
		byAppDay[row.AppID][day] = row
		perApp[row.AppID] = append(perApp[row.AppID], row)
	}

	for i := range resp.Apps {
		app := &resp.Apps[i]
		days := byAppDay[app.AppID]
		for j := range app.Days {
			if row, ok := days[app.Days[j].Day]; ok {
				app.Days[j].Observed = observedDTO(row)
			}
		}
		if rows := perApp[app.AppID]; len(rows) > 0 {
			app.Observed = mergeObserved(rows)
		}
	}
	return nil
}

func observedDTO(row db.ObservedUsage) *dto.ObservedUsage {
	return &dto.ObservedUsage{
		CPUMillicoresAvg: row.CPUMillicoresAvg,
		CPUMillicoresMax: row.CPUMillicoresMax,
		MemoryBytesAvg:   row.MemoryBytesAvg,
		MemoryBytesMax:   row.MemoryBytesMax,
		SampleCount:      row.SampleCount,
	}
}

// mergeObserved combines per-day summaries into a window summary. The
// average is weighted by sample count — an unweighted mean of daily
// means would overweight a day with only a handful of samples.
func mergeObserved(rows []db.ObservedUsage) *dto.ObservedUsage {
	out := &dto.ObservedUsage{}
	var cpuWeighted, memWeighted int64
	for _, row := range rows {
		out.SampleCount += row.SampleCount
		cpuWeighted += int64(row.CPUMillicoresAvg) * int64(row.SampleCount)
		memWeighted += row.MemoryBytesAvg * int64(row.SampleCount)
		if row.CPUMillicoresMax > out.CPUMillicoresMax {
			out.CPUMillicoresMax = row.CPUMillicoresMax
		}
		if row.MemoryBytesMax > out.MemoryBytesMax {
			out.MemoryBytesMax = row.MemoryBytesMax
		}
	}
	if out.SampleCount > 0 {
		out.CPUMillicoresAvg = int(cpuWeighted / int64(out.SampleCount))
		out.MemoryBytesAvg = memWeighted / int64(out.SampleCount)
	}
	return out
}

func addDay(perApp map[string]map[string]dto.UsageDay, appID string, day dto.UsageDay) {
	if perApp[appID] == nil {
		perApp[appID] = map[string]dto.UsageDay{}
	}
	perApp[appID][day.Day] = day
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// accumulate folds an already-rendered day back into running totals. It
// reads the exact integer fields, never the float renderings, so summing
// a month does not accumulate rounding error.
func accumulate(into *Totals, d dto.UsageTotals) {
	into.CPUMillicoreSeconds += d.CPUMillicoreSeconds
	into.GPUCountSeconds += d.GPUCountSeconds
	into.PodSeconds += d.PodSeconds
	into.EstimatedSeconds += d.EstimatedSeconds
	if d.MemoryByteSeconds != "" {
		if v, ok := new(big.Int).SetString(d.MemoryByteSeconds, 10); ok {
			into.MemoryByteSeconds.Add(into.MemoryByteSeconds, v)
		}
	}
}

func totalsFrom(t Totals) dto.UsageTotals {
	mem := t.MemoryByteSeconds
	if mem == nil {
		mem = big.NewInt(0)
	}
	out := dto.UsageTotals{
		CPUMillicoreSeconds: t.CPUMillicoreSeconds,
		MemoryByteSeconds:   mem.String(),
		GPUCountSeconds:     t.GPUCountSeconds,
		PodSeconds:          t.PodSeconds,
		EstimatedSeconds:    t.EstimatedSeconds,
		CPUCoreHours:        float64(t.CPUMillicoreSeconds) / 1000 / 3600,
		GPUHours:            float64(t.GPUCountSeconds) / 3600,
		PodHours:            float64(t.PodSeconds) / 3600,
	}
	if t.PodSeconds > 0 {
		out.EstimatedRatio = float64(t.EstimatedSeconds) / float64(t.PodSeconds)
	}
	// big.Int → float only for the human-facing rendering; the exact
	// value stays in MemoryByteSeconds.
	gib := new(big.Float).SetInt(mem)
	gib.Quo(gib, big.NewFloat(1<<30))
	gib.Quo(gib, big.NewFloat(3600))
	out.MemoryGiBHours, _ = gib.Float64()
	return out
}

func intervalDTO(in db.AllocationInterval) dto.UsageInterval {
	out := dto.UsageInterval{
		PodUID:        in.PodUID,
		AppID:         in.AppID,
		PodName:       in.PodName,
		Namespace:     in.Namespace,
		CPUMillicores: in.CPUMillicores,
		MemoryBytes:   in.MemoryBytes,
		GPUCount:      in.GPUCount,
		GPUType:       in.GPUType,
		StartedAt:     in.StartedAt.UTC().Format(time.RFC3339),
		Estimated:     in.Estimated,
		CloseReason:   in.CloseReason,
	}
	if in.EndedAt != nil {
		s := in.EndedAt.UTC().Format(time.RFC3339)
		out.EndedAt = &s
	}
	return out
}
