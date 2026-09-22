package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	TrackSchemaVersion = SchemaVersion
	TrackPlanned       = "planned"
	TrackActive        = "active"
	TrackReady         = "ready"
	TrackReviewPending = "review_pending"
	TrackAccepted      = "accepted"
	TrackStale         = "stale"
	TrackCancelled     = "cancelled"
	TrackArchived      = "archived"
	TrackMaxTasks      = 256
)

var trackIDRE = regexp.MustCompile(`^([A-Z]{3})-TRK([1-9][0-9]*)$`)

type TrackTaskSnapshot struct {
	Key            string `json:"key"`
	Revision       int    `json:"revision"`
	RevisionSHA256 string `json:"revision_sha256"`
}

type TrackReview struct {
	Head          string              `json:"head"`
	Tree          string              `json:"tree"`
	Digest        string              `json:"digest"`
	TrackRevision int                 `json:"track_revision"`
	Tasks         []TrackTaskSnapshot `json:"tasks"`
	SubmittedAt   time.Time           `json:"submitted_at"`
	SubmittedBy   string              `json:"submitted_by"`
}

type Track struct {
	SchemaVersion   int          `json:"schema_version"`
	ID              string       `json:"id"`
	ProjectID       string       `json:"project_id"`
	Revision        int          `json:"revision"`
	Milestone       string       `json:"milestone"`
	Title           string       `json:"title"`
	Summary         string       `json:"summary,omitempty"`
	Tasks           []string     `json:"tasks"`
	DispatchedTasks []string     `json:"dispatched_tasks,omitempty"`
	Status          string       `json:"status"`
	Review          *TrackReview `json:"review,omitempty"`
	CancelledAt     *time.Time   `json:"cancelled_at,omitempty"`
	CancelledBy     string       `json:"cancelled_by,omitempty"`
	CancelledReason string       `json:"cancelled_reason,omitempty"`
	CreatedBy       string       `json:"created_by"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedBy       string       `json:"updated_by"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

func FormatTrackID(projectCode string, number uint64) (string, error) {
	if err := ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if number == 0 || number > MaxSafeInteger {
		return "", fmt.Errorf("invalid Track number")
	}
	return fmt.Sprintf("%s-TRK%d", projectCode, number), nil
}

func ParseTrackID(value string) (string, uint64, error) {
	matches := trackIDRE.FindStringSubmatch(value)
	if len(matches) != 3 {
		return "", 0, fmt.Errorf("invalid Track identifier")
	}
	var number uint64
	if _, err := fmt.Sscanf(matches[2], "%d", &number); err != nil || number == 0 || number > MaxSafeInteger {
		return "", 0, fmt.Errorf("invalid Track number")
	}
	return matches[1], number, nil
}

func ValidateTrackID(value string) error {
	_, _, err := ParseTrackID(value)
	return err
}

func TrackStatuses() []string {
	return []string{TrackPlanned, TrackActive, TrackReady, TrackReviewPending, TrackAccepted, TrackStale, TrackCancelled, TrackArchived}
}

func ValidateTrack(v Track) error {
	if v.SchemaVersion != TrackSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || ValidateTrackID(v.ID) != nil {
		return fmt.Errorf("invalid Track identity")
	}
	projectCode, _, err := ParseTrackID(v.ID)
	if err != nil {
		return fmt.Errorf("invalid Track identifier")
	}
	milestoneCode, _, milestoneErr := ParseMilestoneID(v.Milestone)
	if milestoneErr != nil || milestoneCode != projectCode {
		return fmt.Errorf("invalid Track Milestone")
	}
	if len([]rune(v.Title)) < 3 || len([]rune(v.Title)) > 128 || len([]rune(v.Summary)) > 256 {
		return fmt.Errorf("invalid Track content")
	}
	if len(v.Tasks) == 0 || len(v.Tasks) > TrackMaxTasks {
		return fmt.Errorf("Track task membership must contain 1-%d Tasks", TrackMaxTasks)
	}
	seen := make(map[string]struct{}, len(v.Tasks))
	for _, taskID := range v.Tasks {
		if ValidateCanonicalTaskID(taskID) != nil {
			return fmt.Errorf("invalid Track task %q", taskID)
		}
		if _, exists := seen[taskID]; exists {
			return fmt.Errorf("duplicate Track task %q", taskID)
		}
		seen[taskID] = struct{}{}
	}
	dispatched := make(map[string]struct{}, len(v.DispatchedTasks))
	for _, taskID := range v.DispatchedTasks {
		if _, member := seen[taskID]; !member {
			return fmt.Errorf("Track dispatch history contains non-member Task %q", taskID)
		}
		if _, exists := dispatched[taskID]; exists {
			return fmt.Errorf("duplicate Track dispatched Task %q", taskID)
		}
		dispatched[taskID] = struct{}{}
	}
	if !containsTrackStatus(v.Status) {
		return fmt.Errorf("invalid Track status")
	}
	if v.Status == TrackReviewPending || v.Status == TrackAccepted || v.Status == TrackStale {
		if err := ValidateTrackReview(v.Review, v.Revision); err != nil {
			return err
		}
		if len(v.Review.Tasks) != len(v.Tasks) {
			return fmt.Errorf("Track review membership does not match Track membership")
		}
		for index, taskID := range v.Tasks {
			if v.Review.Tasks[index].Key != taskID {
				return fmt.Errorf("Track review membership does not match Track membership")
			}
		}
	}
	if v.Status == TrackCancelled || v.CancelledAt != nil || v.CancelledBy != "" || v.CancelledReason != "" {
		if v.Status != TrackCancelled && v.Status != TrackArchived {
			return fmt.Errorf("non-terminal Track contains cancellation metadata")
		}
		if v.CancelledAt == nil || v.CancelledBy == "" || strings.TrimSpace(v.CancelledReason) == "" {
			return fmt.Errorf("cancelled Track requires cancellation metadata")
		}
	}
	if v.Revision < 1 || v.CreatedBy == "" || v.UpdatedBy == "" || strings.ContainsAny(v.CreatedBy+v.UpdatedBy, "\x00\r\n") || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid Track metadata")
	}
	return nil
}

func ValidateTrackReview(review *TrackReview, trackRevision int) error {
	if review == nil || ValidateCommitSHA(review.Head) != nil || ValidateCommitSHA(review.Tree) != nil || ValidateSHA256(review.Digest) != nil || review.TrackRevision < 1 || review.TrackRevision != trackRevision || review.SubmittedAt.IsZero() || review.SubmittedBy == "" || strings.ContainsAny(review.SubmittedBy, "\x00\r\n") {
		return fmt.Errorf("invalid Track review snapshot")
	}
	if len(review.Tasks) == 0 || len(review.Tasks) > TrackMaxTasks {
		return fmt.Errorf("invalid Track review task snapshot")
	}
	seen := make(map[string]struct{}, len(review.Tasks))
	for _, task := range review.Tasks {
		if ValidateCanonicalTaskID(task.Key) != nil || task.Revision < 1 || ValidateSHA256(task.RevisionSHA256) != nil {
			return fmt.Errorf("invalid Track review Task snapshot")
		}
		if _, exists := seen[task.Key]; exists {
			return fmt.Errorf("duplicate Track review Task snapshot")
		}
		seen[task.Key] = struct{}{}
	}
	return nil
}

func NewTrack(projectID, id, milestone, title, summary string, tasks []string, createdBy string, now time.Time) (Track, error) {
	track := Track{
		SchemaVersion: TrackSchemaVersion,
		ID:            id,
		ProjectID:     projectID,
		Revision:      1,
		Milestone:     milestone,
		Title:         title,
		Summary:       summary,
		Tasks:         append([]string{}, tasks...),
		Status:        TrackPlanned,
		CreatedBy:     createdBy,
		CreatedAt:     now.UTC(),
		UpdatedBy:     createdBy,
		UpdatedAt:     now.UTC(),
	}
	if err := ValidateTrack(track); err != nil {
		return Track{}, err
	}
	return track, nil
}

func containsTrackStatus(status string) bool {
	for _, value := range TrackStatuses() {
		if value == status {
			return true
		}
	}
	return false
}
