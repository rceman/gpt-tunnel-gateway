package onboarding

import "time"

// monotonicReceiptTimestamp keeps a newly persisted receipt transition at or
// after the timestamps already proven by the prior durable receipt.
func monotonicReceiptTimestamp(receipt Receipt, candidate time.Time) (time.Time, error) {
	candidate = candidate.UTC()
	values := []string{receipt.Timestamps.StartedAt, receipt.Timestamps.UpdatedAt}
	if receipt.Timestamps.PreparedAt != nil {
		values = append(values, *receipt.Timestamps.PreparedAt)
	}
	if receipt.Timestamps.HubCommittedAt != nil {
		values = append(values, *receipt.Timestamps.HubCommittedAt)
	}
	for _, value := range values {
		parsed, err := parseReceiptTime(value)
		if err != nil {
			return time.Time{}, err
		}
		if candidate.Before(parsed) {
			candidate = parsed
		}
	}
	return candidate, nil
}
