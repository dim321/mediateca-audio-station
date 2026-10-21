package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnqueueSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 9, 2, 2, 0, 1, 0, time.UTC)
	if err := q.Enqueue(Event{ScreenID: 42, MediaAssetID: 19, StartedAt: started}, "42:19:2026-09-02T02:00:00Z"); err != nil {
		t.Fatal(err)
	}
	q2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !q2.Played("42:19:2026-09-02T02:00:00Z") {
		t.Fatal("played key missing")
	}
	ev, ok, err := q2.Peek()
	if err != nil || !ok || ev.MediaAssetID != 19 || !ev.StartedAt.Equal(started) {
		t.Fatalf("%+v ok=%v err=%v", ev, ok, err)
	}
	if err := q2.RemoveFirst(); err != nil {
		t.Fatal(err)
	}
	_, ok, err = q2.Peek()
	if err != nil || ok {
		t.Fatal(err, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "played.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOutcomeTable(t *testing.T) {
	if OutcomeFor(201, false) != OutcomeRemove || OutcomeFor(401, false) != OutcomePause {
		t.Fatal("201/401")
	}
	if OutcomeFor(404, false) != OutcomeDrop || OutcomeFor(422, false) != OutcomeDrop {
		t.Fatal("drop")
	}
	if OutcomeFor(500, false) != OutcomeRetry || OutcomeFor(0, true) != OutcomeRetry {
		t.Fatal("retry")
	}
}
