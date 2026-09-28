package incident

import (
	"sort"
	"time"
)

// WindowRef is the bounded, non-ended window index stored on a monitor.
type WindowRef struct {
	WindowID string `dynamodbav:"windowId"`
	StartAt  string `dynamodbav:"startAt"`
	EndAt    string `dynamodbav:"endAt"`
}

func Contains(w WindowRef, at time.Time) bool {
	start, e1 := time.Parse(time.RFC3339Nano, w.StartAt)
	end, e2 := time.Parse(time.RFC3339Nano, w.EndAt)
	return e1 == nil && e2 == nil && !at.Before(start) && at.Before(end)
}

func Active(windows []WindowRef, at time.Time) string {
	for _, w := range windows {
		if Contains(w, at) {
			return w.WindowID
		}
	}
	return ""
}

func Next(windows []WindowRef, at time.Time) string {
	for _, w := range windows {
		start, err := time.Parse(time.RFC3339Nano, w.StartAt)
		if err == nil && start.After(at) {
			return w.WindowID
		}
	}
	return ""
}

func Prune(windows []WindowRef, at time.Time) []WindowRef {
	result := make([]WindowRef, 0, len(windows))
	for _, w := range windows {
		end, err := time.Parse(time.RFC3339Nano, w.EndAt)
		if err == nil && end.After(at) {
			result = append(result, w)
		}
	}
	return result
}

func Overlaps(windows []WindowRef, start, end time.Time) bool {
	for _, w := range windows {
		a, ea := time.Parse(time.RFC3339Nano, w.StartAt)
		b, eb := time.Parse(time.RFC3339Nano, w.EndAt)
		if ea == nil && eb == nil && start.Before(b) && a.Before(end) {
			return true
		}
	}
	return false
}

func Insert(windows []WindowRef, w WindowRef) []WindowRef {
	result := append(append([]WindowRef(nil), windows...), w)
	sort.Slice(result, func(i, j int) bool { return result[i].StartAt < result[j].StartAt })
	return result
}
