package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// PlanCutoverInput is intentionally separate from ordinary plan reads and
// writes. It is the sole owner-invoked operation that accepts the known v1
// plan record.
type PlanCutoverInput struct {
	ProjectID string `json:"project_id"`
	UpdatedBy string `json:"updated_by"`
	WriteOptions
}

type legacyPlanV1 struct {
	SchemaVersion int       `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	Revision      int       `json:"revision"`
	Summary       string    `json:"summary"`
	Body          string    `json:"body"`
	ActiveTaskID  string    `json:"active_task_id,omitempty"`
	ActiveRunID   string    `json:"active_run_id,omitempty"`
	UpdatedBy     string    `json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type legacyPlanBlock struct {
	Level   int
	Title   string
	Content string
}

var markdownHeadingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
var markdownListRE = regexp.MustCompile(`^\s*(?:[-*+]|[0-9]+[.)])\s+(?:\[[ xX]\]\s*)?(.+?)\s*$`)
var currentQueueEntryRE = regexp.MustCompile(`^\s*(P[0-9]+)\s+—\s+.+?\s*$`)
var inlineObjectiveRE = regexp.MustCompile(`(?i)^\s*(?:current objective|objective)\s*:\s*(.+?)\s*$`)

func normalizeHeading(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func planSectionID(title string, used map[string]bool) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "section"
	}
	if len(id) > 58 {
		id = strings.Trim(id[:58], "-")
	}
	base := id
	for suffix := 2; used[id]; suffix++ {
		id = base + "-" + strconv.Itoa(suffix)
	}
	used[id] = true
	return id
}

func splitLegacyPlanBody(body string) ([]legacyPlanBlock, error) {
	lines := strings.Split(body, "\n")
	blocks := []legacyPlanBlock{}
	current := -1
	for _, line := range lines {
		match := markdownHeadingRE.FindStringSubmatch(line)
		if match != nil {
			blocks = append(blocks, legacyPlanBlock{
				Level: len(match[1]),
				Title: strings.TrimSpace(match[2]),
			})
			current = len(blocks) - 1
			continue
		}
		if current < 0 {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(blocks) == 0 || blocks[0].Title != "Overview" {
				blocks = append([]legacyPlanBlock{{Level: 0, Title: "Overview"}}, blocks...)
				current = 0
			}
		}
		if current >= 0 {
			if blocks[current].Content != "" || line != "" {
				blocks[current].Content += line + "\n"
			}
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("legacy plan body is empty")
	}
	for i := range blocks {
		blocks[i].Content = strings.TrimSpace(blocks[i].Content)
	}
	return blocks, nil
}

func shortSectionDescription(title, content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-+*0123456789.) "))
		if len(line) > 500 {
			line = line[:500]
		}
		return line
	}
	return title
}

func legacyQueue(blocks []legacyPlanBlock) []string {
	queue := []string{}
	used := map[string]bool{}
	for _, block := range blocks {
		heading := normalizeHeading(block.Title)
		if heading != "queue" && heading != "backlog" && heading != "next steps" && heading != "queue — workflow and documentation before optional features" {
			continue
		}
		for _, line := range strings.Split(block.Content, "\n") {
			if match := currentQueueEntryRE.FindStringSubmatch(line); match != nil {
				queue = append(queue, match[1])
				continue
			}
			match := markdownListRE.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			item := strings.TrimSpace(match[1])
			id := planSectionID(item, used)
			queue = append(queue, id)
		}
	}
	return queue
}

func containsLine(lines map[string]int, line string) bool {
	return lines[strings.TrimSpace(line)] > 0
}

func buildCutover(legacy legacyPlanV1, updatedBy string, now time.Time) (model.Plan, []model.PlanSection, error) {
	if legacy.SchemaVersion != model.SchemaVersion || legacy.ProjectID == "" || legacy.Revision < 1 || legacy.Summary == "" {
		return model.Plan{}, nil, fmt.Errorf("invalid schema-v1 plan for explicit cutover")
	}
	blocks, err := splitLegacyPlanBody(legacy.Body)
	if err != nil {
		return model.Plan{}, nil, err
	}
	used := map[string]bool{}
	sections := make([]model.PlanSection, 0, len(blocks))
	indexes := make([]model.PlanSectionIndex, 0, len(blocks))
	title := "Migrated plan"
	objective := ""
	for _, block := range blocks {
		if block.Level == 1 && title == "Migrated plan" {
			title = block.Title
		}
		heading := normalizeHeading(block.Title)
		if heading == "current objective" || heading == "objective" {
			objective = strings.TrimSpace(block.Content)
		}
		if objective == "" {
			for _, line := range strings.Split(block.Content, "\n") {
				if match := inlineObjectiveRE.FindStringSubmatch(line); match != nil {
					objective = strings.TrimSpace(match[1])
					break
				}
			}
		}
		id := planSectionID(block.Title, used)
		section := model.PlanSection{SchemaVersion: model.PlanSchemaVersion, ProjectID: legacy.ProjectID, ID: id, Revision: 1, Title: block.Title, ShortDescription: shortSectionDescription(block.Title, block.Content), Description: block.Content, UpdatedBy: updatedBy, UpdatedAt: now}
		if err := model.ValidatePlanSection(section); err != nil {
			return model.Plan{}, nil, fmt.Errorf("cutover section %q invalid: %w", block.Title, err)
		}
		sections = append(sections, section)
		indexes = append(indexes, model.PlanSectionIndex{ID: section.ID, Title: section.Title, ShortDescription: section.ShortDescription, Revision: section.Revision})
	}
	plan := model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: legacy.ProjectID, Revision: legacy.Revision, Title: title, Summary: legacy.Summary, CurrentObjective: objective, Queue: legacyQueue(blocks), Sections: indexes, ActiveTaskID: legacy.ActiveTaskID, ActiveRunID: legacy.ActiveRunID, UpdatedBy: updatedBy, UpdatedAt: now}
	if err := model.ValidatePlan(plan); err != nil {
		return model.Plan{}, nil, fmt.Errorf("cutover manifest invalid: %w", err)
	}
	if err := proveLegacyBodyPreserved(legacy.Body, sections); err != nil {
		return model.Plan{}, nil, err
	}
	return plan, sections, nil
}

func proveLegacyBodyPreserved(body string, sections []model.PlanSection) error {
	contentLines := map[string]int{}
	headingLines := map[string]int{}
	for _, section := range sections {
		headingLines[strings.TrimSpace(section.Title)]++
		for _, line := range strings.Split(section.Description, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				contentLines[trimmed]++
			}
		}
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		match := markdownHeadingRE.FindStringSubmatch(trimmed)
		if match != nil {
			if headingLines[strings.TrimSpace(match[2])] == 0 {
				return fmt.Errorf("cutover semantic preservation failed for heading %q", strings.TrimSpace(match[2]))
			}
			continue
		}
		if !containsLine(contentLines, trimmed) {
			return fmt.Errorf("cutover semantic preservation failed for content %q", trimmed)
		}
	}
	return nil
}

// PlanCutover performs the only supported schema-v1 conversion. A schema-v2
// plan is rejected so the operation is strictly one-time and never acts as a
// reader fallback.
func (s *Service) PlanCutover(ctx context.Context, in PlanCutoverInput) (OperationResult, error) {
	if err := rejectPlanMutationAfterTrainV2(ctx, s, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	if in.ProjectID == "" || in.UpdatedBy == "" {
		return OperationResult{}, fmt.Errorf("project_id and updated_by are required")
	}
	if _, err := s.ProjectRead(ctx, in.ProjectID); err != nil {
		return OperationResult{}, err
	}
	data, err := s.Hub.ReadFile(ctx, s.planPath(in.ProjectID))
	if err != nil {
		return OperationResult{}, err
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return OperationResult{}, fmt.Errorf("read plan schema: %w", err)
	}
	if header.SchemaVersion != model.SchemaVersion {
		return OperationResult{}, fmt.Errorf("plan is not schema-v1; explicit cutover already completed or unsupported")
	}
	var legacy legacyPlanV1
	if err := decodeStrict(data, &legacy); err != nil {
		return OperationResult{}, fmt.Errorf("read schema-v1 plan for explicit cutover: %w", err)
	}
	now := time.Now().UTC()
	tx, err := s.Hub.Transact(ctx, in.ExpectedHubRevision, "gateway: explicit plan schema-v1 cutover "+in.ProjectID, func(w string) ([]string, error) {
		var current legacyPlanV1
		if err := readWorktreeJSON(w, s.planPath(in.ProjectID), &current); err != nil {
			return nil, err
		}
		if current.SchemaVersion != model.SchemaVersion {
			return nil, fmt.Errorf("plan cutover source changed or is already schema-v2")
		}
		plan, sections, err := buildCutover(current, in.UpdatedBy, now)
		if err != nil {
			return nil, err
		}
		paths := []string{}
		for _, section := range sections {
			path := s.planSectionPath(in.ProjectID, section.ID)
			if err := hub.WriteJSON(w, path, section); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		planPath := s.planPath(in.ProjectID)
		if err := hub.WriteJSON(w, planPath, plan); err != nil {
			return nil, err
		}
		var verified model.Plan
		if err := readWorktreeJSON(w, planPath, &verified); err != nil {
			return nil, fmt.Errorf("cutover manifest verification: %w", err)
		}
		if err := model.ValidatePlan(verified); err != nil {
			return nil, err
		}
		paths = append(paths, planPath)
		return paths, nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "cut over",
	}, nil
}
