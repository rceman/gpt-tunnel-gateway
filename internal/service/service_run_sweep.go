package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) RunSweep(ctx context.Context) (SweepResult, error) {
	out := SweepResult{Items: []SweepItem{}}
	projects, err := s.ProjectList(ctx)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC()
	for _, project := range projects {
		runs, err := s.RunList(ctx, project.ID)
		if err != nil {
			return out, err
		}
		for _, run := range runs {
			if model.ValidateCanonicalRunID(run.ID) != nil || run.GatewayID != s.Config.GatewayID || !operationalActiveRun(run) {
				continue
			}
			out.Checked++
			start := run.CreatedAt
			if run.DispatchedAt != nil {
				start = *run.DispatchedAt
			}
			if run.LastRepromptAt != nil {
				start = *run.LastRepromptAt
			}
			if run.Status != "cancel_requested" {
				if e := s.observeResumeProgress(ctx, run, now); e != nil {
					out.Items = append(out.Items, SweepItem{
						RunID:  run.ID,
						Action: "error",
						Status: run.Status,
						Error:  "liveness observation failed",
					})
					continue
				}
				if _, resumeErr := s.runResume(ctx, run.ID, true); resumeErr == nil {
					out.Items = append(out.Items, SweepItem{
						RunID:  run.ID,
						Action: "resume",
						Status: run.Status,
					})
					continue
				}
			}
			if now.Sub(start) < time.Duration(s.Config.RunTimeoutSeconds)*time.Second {
				continue
			}
			task, e := s.findTask(ctx, run.TaskID)
			if e != nil {
				out.Items = append(out.Items, SweepItem{
					RunID:  run.ID,
					Action: "error",
					Status: run.Status,
					Error:  e.Error(),
				})
				continue
			}
			if run.Status == "cancel_requested" {
				expected, e := s.hubRevision(ctx)
				if e != nil {
					return out, e
				}
				tx, e := s.failRun(ctx, run, task, "failed", "cooperative cancellation timed out", expected)
				_ = tx
				item := SweepItem{
					RunID:  run.ID,
					Action: "finalize_cancelled",
					Status: "failed",
				}
				if e != nil {
					item.Error = e.Error()
				}
				out.Items = append(out.Items, item)
				continue
			}
			if run.RepromptCount < 1 {
				_, resumeErr := s.runResume(ctx, run.ID, true)
				item := SweepItem{
					RunID:  run.ID,
					Action: "resume",
					Status: run.Status,
				}
				if resumeErr != nil {
					// A stale run without confirmed compaction is not silently
					// reprompted.  It remains visible for an explicit operator/GPT
					// decision instead of receiving a bare continue message.
					item.Action = "review"
					item.Error = "automatic resume not performed"
				}
				out.Items = append(out.Items, item)
				continue
			}
			expected, e := s.hubRevision(ctx)
			if e != nil {
				return out, e
			}
			tx, e := s.failRun(ctx, run, task, "failed", "agent completion was not finalized before timeout", expected)
			_ = tx
			item := SweepItem{
				RunID:  run.ID,
				Action: "finalize_timeout",
				Status: "failed",
			}
			if e != nil {
				item.Error = e.Error()
			}
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}

func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return errors.Is(err, os.ErrNotExist) || strings.Contains(text, "does not exist") || strings.Contains(text, "not found") || strings.Contains(text, "exists on disk, but not in")
}
