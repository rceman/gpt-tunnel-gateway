package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	milestonePlanMaxTracks       = 256
	milestonePlanMaxMarkdownSize = 32 * 1024
)

var milestonePlanInlineEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"`", "\\`",
	"*", "\\*",
	"_", "\\_",
	"[", "\\[",
	"]", "\\]",
	"(", "\\(",
	")", "\\)",
	"<", "\\<",
	">", "\\>",
	"#", "\\#",
	"|", "\\|",
)

func (s *Service) MilestoneLifecyclePlan(ctx context.Context, projectID, key string) (string, error) {
	view, err := s.MilestoneLifecycleView(ctx, projectID, key, 0)
	if err != nil {
		return "", err
	}
	tracks := make([]TrackView, 0)
	seenTracks := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	cursor := ""
	pageCount := 0
	for {
		pageCount++
		if pageCount > milestonePlanMaxTracks {
			return "", fmt.Errorf("Milestone plan exceeds bounded Track page limit %d", milestonePlanMaxTracks)
		}
		page, err := s.TrackLifecycleListQuery(ctx, projectID, "", "", key, cursor, false)
		if err != nil {
			return "", err
		}
		for _, track := range page.Tracks {
			if track.Track.Milestone != key {
				return "", fmt.Errorf("Track %q does not belong to requested Milestone", track.Track.ID)
			}
			if track.Track.Status == model.TrackArchived {
				continue
			}
			if _, exists := seenTracks[track.Track.ID]; exists {
				return "", fmt.Errorf("duplicate Track %q in Milestone plan query", track.Track.ID)
			}
			if len(tracks) >= milestonePlanMaxTracks {
				return "", fmt.Errorf("Milestone plan exceeds bounded Track limit %d", milestonePlanMaxTracks)
			}
			seenTracks[track.Track.ID] = struct{}{}
			tracks = append(tracks, track)
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return "", fmt.Errorf("Milestone plan Track query returned an invalid continuation")
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			return "", fmt.Errorf("Milestone plan Track query repeated a continuation")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return renderMilestonePlan(view, tracks)
}

func renderMilestonePlan(view MilestoneView, tracks []TrackView) (string, error) {
	milestone := view.Milestone
	if len(tracks) > milestonePlanMaxTracks {
		return "", fmt.Errorf("Milestone plan exceeds bounded Track limit %d", milestonePlanMaxTracks)
	}
	tasks := make(map[string]MilestoneTaskProjection, len(view.Tasks))
	archivedCount := 0
	for _, task := range view.Tasks {
		if _, exists := tasks[task.Key]; exists {
			return "", fmt.Errorf("duplicate Task %q in Milestone plan view", task.Key)
		}
		tasks[task.Key] = task
		if task.Archived {
			archivedCount++
		}
	}
	if len(tasks) != len(milestone.Tasks) {
		return "", fmt.Errorf("Milestone plan Task projection is incomplete")
	}
	currentTracks := make([]TrackView, 0, len(tracks))
	for _, track := range tracks {
		if track.Track.Status == model.TrackArchived {
			continue
		}
		if track.Track.Milestone != milestone.ID {
			return "", fmt.Errorf("Track %q does not belong to Milestone %q", track.Track.ID, milestone.ID)
		}
		if _, _, err := model.ParseTrackID(track.Track.ID); err != nil {
			return "", fmt.Errorf("invalid Track in Milestone plan: %w", err)
		}
		currentTracks = append(currentTracks, track)
	}
	sort.Slice(currentTracks, func(i, j int) bool {
		leftProject, leftNumber, _ := model.ParseTrackID(currentTracks[i].Track.ID)
		rightProject, rightNumber, _ := model.ParseTrackID(currentTracks[j].Track.ID)
		if leftProject != rightProject {
			return leftProject < rightProject
		}
		if leftNumber != rightNumber {
			return leftNumber < rightNumber
		}
		return currentTracks[i].Track.ID < currentTracks[j].Track.ID
	})
	ownerOrder := append([]TrackView{}, currentTracks...)
	for _, track := range ownerOrder {
		if _, err := milestonePlanTrackOwnerRank(track.Track.Status); err != nil {
			return "", err
		}
	}
	sort.Slice(ownerOrder, func(i, j int) bool {
		leftRank, _ := milestonePlanTrackOwnerRank(ownerOrder[i].Track.Status)
		rightRank, _ := milestonePlanTrackOwnerRank(ownerOrder[j].Track.Status)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftProject, leftNumber, _ := model.ParseTrackID(ownerOrder[i].Track.ID)
		rightProject, rightNumber, _ := model.ParseTrackID(ownerOrder[j].Track.ID)
		if leftProject != rightProject {
			return leftProject < rightProject
		}
		if leftNumber != rightNumber {
			return leftNumber > rightNumber
		}
		return ownerOrder[i].Track.ID > ownerOrder[j].Track.ID
	})
	owners := make(map[string]string)
	for _, track := range ownerOrder {
		if track.Track.Status == model.TrackCancelled {
			continue
		}
		for _, taskKey := range track.Track.Tasks {
			task, exists := tasks[taskKey]
			if !exists {
				return "", fmt.Errorf("Track %q contains a Task outside the Milestone projection", track.Track.ID)
			}
			if task.Archived {
				continue
			}
			if _, assigned := owners[taskKey]; !assigned {
				owners[taskKey] = track.Track.ID
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n**Status:** %s\n", milestone.ID, milestonePlanInline(milestone.Title), milestone.Status)
	if summary := milestonePlanInline(milestone.Summary); summary != "" {
		fmt.Fprintf(&b, "**Summary:** %s\n", summary)
	}
	for _, track := range currentTracks {
		fmt.Fprintf(&b, "\n## %s — %s\n\n**Status:** %s\n", track.Track.ID, milestonePlanInline(track.Track.Title), track.Track.Status)
		if summary := milestonePlanInline(track.Track.Summary); summary != "" {
			fmt.Fprintf(&b, "**Summary:** %s\n", summary)
		}
		if track.Track.Status == model.TrackCancelled {
			b.WriteString("Cancelled Track membership is historical; Tasks are shown under current Tracks or Ungrouped tasks.\n")
			continue
		}
		for _, taskKey := range track.Track.Tasks {
			if owners[taskKey] != track.Track.ID {
				continue
			}
			if err := writeMilestonePlanTask(&b, tasks[taskKey]); err != nil {
				return "", err
			}
		}
	}
	b.WriteString("\n## Ungrouped tasks\n")
	ungrouped := make([]MilestoneTaskProjection, 0, len(tasks))
	for _, task := range view.Tasks {
		if !task.Archived && owners[task.Key] == "" {
			ungrouped = append(ungrouped, task)
		}
	}
	priorityRank := make(map[string]int, len(ungrouped))
	for _, task := range ungrouped {
		rank, err := model.TaskPriorityRank(task.Priority)
		if err != nil {
			return "", fmt.Errorf("Milestone Task %q has no canonical priority: %w", task.Key, err)
		}
		priorityRank[task.Key] = rank
	}
	sort.Slice(ungrouped, func(i, j int) bool {
		if priorityRank[ungrouped[i].Key] != priorityRank[ungrouped[j].Key] {
			return priorityRank[ungrouped[i].Key] < priorityRank[ungrouped[j].Key]
		}
		return ungrouped[i].Key < ungrouped[j].Key
	})
	if len(ungrouped) == 0 {
		b.WriteString("\nNone.\n")
	} else {
		b.WriteByte('\n')
		for _, task := range ungrouped {
			if err := writeMilestonePlanTask(&b, task); err != nil {
				return "", err
			}
		}
	}
	if archivedCount > 0 {
		fmt.Fprintf(&b, "\n(%d archived members omitted)\n", archivedCount)
	}
	markdown := b.String()
	if len(markdown) > milestonePlanMaxMarkdownSize {
		return "", fmt.Errorf("Milestone plan exceeds bounded Markdown limit of %d bytes", milestonePlanMaxMarkdownSize)
	}
	return markdown, nil
}

func milestonePlanTrackOwnerRank(status string) (int, error) {
	switch status {
	case model.TrackPlanned, model.TrackActive, model.TrackReady, model.TrackReviewPending, model.TrackStale:
		return 0, nil
	case model.TrackAccepted:
		return 1, nil
	case model.TrackCancelled:
		return 2, nil
	case model.TrackArchived:
		return 3, nil
	default:
		return 0, fmt.Errorf("invalid Track status %q in Milestone plan", status)
	}
}

func milestonePlanInline(value string) string {
	return milestonePlanInlineEscaper.Replace(strings.Join(strings.Fields(value), " "))
}

func writeMilestonePlanTask(b *strings.Builder, task MilestoneTaskProjection) error {
	if task.Archived {
		return nil
	}
	priority, err := model.NormalizeTaskPriority(task.Priority)
	if err != nil || priority == "" {
		return fmt.Errorf("Milestone Task %q has no canonical priority", task.Key)
	}
	symbol, err := model.TaskStatusSymbol(task.Status)
	if err != nil {
		return fmt.Errorf("Milestone Task %q has unsupported status: %w", task.Key, err)
	}
	fmt.Fprintf(b, "%s [%s] %s — %s\n", symbol, priority, task.Key, milestonePlanInline(task.Title))
	return nil
}
