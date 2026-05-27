package leader

import "testing"

func TestNopObserverIsZeroValueSafe(t *testing.T) {
	var o Observer = NopObserver{}
	o.OnGained("pod-a")
	o.OnLost("pod-a")
	o.OnNewLeader("pod-a", "pod-b")
	o.OnLeaseRenew("acquired")
}

func TestPrometheusProviderEmitsOnViaObserver(t *testing.T) {
	rec := &recordingObserver{}
	p := PrometheusProvider{Observer: rec}
	m := p.NewLeaderMetric()
	m.On("0ops-backend-leader")
	if got, want := rec.renews["acquired"], 1; got != want {
		t.Fatalf("renews[acquired] = %d, want %d", got, want)
	}
}

func TestPrometheusProviderEmitsOffViaObserver(t *testing.T) {
	rec := &recordingObserver{}
	p := PrometheusProvider{Observer: rec}
	m := p.NewLeaderMetric()
	m.Off("0ops-backend-leader")
	if got, want := rec.renews["lost"], 1; got != want {
		t.Fatalf("renews[lost] = %d, want %d", got, want)
	}
}

func TestPrometheusProviderEmitsSlowpathViaObserver(t *testing.T) {
	rec := &recordingObserver{}
	p := PrometheusProvider{Observer: rec}
	m := p.NewLeaderMetric()
	m.SlowpathExercised("0ops-backend-leader")
	if got, want := rec.renews["slow_acquire"], 1; got != want {
		t.Fatalf("renews[slow_acquire] = %d, want %d", got, want)
	}
}

func TestPrometheusProviderToleratesNilObserver(t *testing.T) {
	p := PrometheusProvider{}
	m := p.NewLeaderMetric()
	// Calls must not panic when no Observer is wired.
	m.On("name")
	m.Off("name")
	m.SlowpathExercised("name")
}

type recordingObserver struct {
	gained  []string
	lost    []string
	handovers []struct{ current, next string }
	renews  map[string]int
}

func (r *recordingObserver) OnGained(id string)                    { r.gained = append(r.gained, id) }
func (r *recordingObserver) OnLost(id string)                      { r.lost = append(r.lost, id) }
func (r *recordingObserver) OnNewLeader(currentID, newID string) {
	r.handovers = append(r.handovers, struct{ current, next string }{currentID, newID})
}
func (r *recordingObserver) OnLeaseRenew(outcome string) {
	if r.renews == nil {
		r.renews = map[string]int{}
	}
	r.renews[outcome]++
}
