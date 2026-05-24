package sentinel

import "testing"

func TestClassifyIOBottleneckFromTempSpill(t *testing.T) {
	report := BurstReport{Metrics: map[string]MetricSummary{
		string(MetricTempBytesRate): {Avg: 80 * 1024 * 1024, Max: 240 * 1024 * 1024},
	}}
	got := Classify(report)
	if got.Cause != CauseIOBottleneck {
		t.Fatalf("Classify cause=%s, want %s (%#v)", got.Cause, CauseIOBottleneck, got)
	}
	if got.Confidence < 0.7 {
		t.Fatalf("confidence=%f, want >=0.7", got.Confidence)
	}
}

func TestClassifyIOBottleneckFromWaitProfile(t *testing.T) {
	report := BurstReport{WaitProfile: []WaitBucket{{WaitEventType: "IO", WaitEvent: "DataFileRead", Count: 8, Percentage: 72}}}
	got := Classify(report)
	if got.Cause != CauseIOBottleneck {
		t.Fatalf("Classify cause=%s, want %s (%#v)", got.Cause, CauseIOBottleneck, got)
	}
}

func TestClassifyFromTriggerMapsTempBytesToIO(t *testing.T) {
	got := classifyFromTrigger(TriggerEvent{Metric: string(MetricTempBytesRate), Baseline: 1, Current: 100, Threshold: 10})
	if got.Cause != CauseIOBottleneck {
		t.Fatalf("trigger cause=%s, want %s", got.Cause, CauseIOBottleneck)
	}
}
