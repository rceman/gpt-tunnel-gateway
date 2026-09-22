package sqlitestore

import (
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func validateTrackMutation(previousPayload, payload []byte, kind string) error {
	var previous, next model.Track
	if err := json.Unmarshal(previousPayload, &previous); err != nil {
		return fmt.Errorf("invalid shared Track payload")
	}
	if err := json.Unmarshal(payload, &next); err != nil {
		return fmt.Errorf("invalid shared Track payload")
	}
	if previous.Status == model.TrackAccepted || previous.Status == model.TrackArchived {
		if kind != "track-archive" {
			return fmt.Errorf("accepted or archived Track is immutable")
		}
	}
	if previous.Status == model.TrackCancelled && kind != "track-archive" {
		return fmt.Errorf("cancelled Track is immutable")
	}
	switch kind {
	case "track-update":
		if previous.Status != model.TrackPlanned || next.Status != model.TrackPlanned {
			return fmt.Errorf("Track update is planned-only")
		}
	case "track-append-task", "track-remove-task":
		if previous.Status == model.TrackPlanned || next.Status == model.TrackPlanned {
			return fmt.Errorf("Track membership append/remove is post-start only")
		}
	case "track-submit":
		if next.Status != model.TrackReviewPending || (previous.Status != model.TrackReady && previous.Status != model.TrackStale && previous.Status != model.TrackReviewPending) {
			return fmt.Errorf("Track submit requires a derived ready Track")
		}
	case "track-accept":
		if previous.Status != model.TrackReviewPending || next.Status != model.TrackAccepted {
			return fmt.Errorf("Track accept requires a pending review")
		}
	case "track-cancel":
		if previous.Status != model.TrackPlanned || next.Status != model.TrackCancelled {
			return fmt.Errorf("Track cancel is planned-only")
		}
	case "track-archive":
		if (previous.Status != model.TrackAccepted && previous.Status != model.TrackCancelled) || next.Status != model.TrackArchived {
			return fmt.Errorf("Track archive requires accepted or cancelled status")
		}
	default:
		if previous.Status != next.Status {
			return fmt.Errorf("unsupported Track lifecycle mutation %q", kind)
		}
	}
	if next.Milestone != previous.Milestone {
		return fmt.Errorf("Track Milestone is immutable")
	}
	return nil
}
