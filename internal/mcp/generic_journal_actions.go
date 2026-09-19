package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureJournalActions() {
	s.journalActions.Do(func() { s.journalActionErr = s.registerJournalActions() })
	if s.journalActionErr != nil {
		panic(s.journalActionErr)
	}
}

func (s *Server) registerJournalActions() error {
	register := func(a GenericAction) error {
		a.AuthorityRole = actionRoleWorkflow
		a.SessionBound = true
		a.LocalReceiptOnly = true
		return s.RegisterGenericAction(a)
	}
	if err := register(GenericAction{
		Path:                 "journal/contract",
		Description:          "Read the published contract for one journal stream.",
		InputSchema:          journalContractSchema(),
		ExecutionInputSchema: journalContractSchema(),
		OutputSchema:         journalContractOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.JournalContractInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			return s.Service.JournalContract(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "journal/add",
		Description:          "Append one contract-validated immutable entry to a journal stream.",
		InputSchema:          journalAddSchema(),
		ExecutionInputSchema: journalAddSchema(),
		OutputSchema:         journalAddOutputSchema(),
		Annotations: ToolAnnotations{
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.JournalAddInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			entry, _, err := s.Service.JournalAdd(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": entry.ID}, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "journal/list",
		Description:          "List the bounded keyset-ordered journal entries for the session project.",
		InputSchema:          journalListSchema(),
		ExecutionInputSchema: journalListSchema(),
		OutputSchema:         journalListOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.JournalListInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			page, err := s.Service.JournalList(ctx, in)
			if err != nil {
				return nil, err
			}
			items := make([]any, 0, len(page.Items))
			for _, entry := range page.Items {
				items = append(items, journalEntryPublicProjection(entry))
			}
			result := map[string]any{"items": items}
			if page.HasMore && page.NextCursor != "" {
				result["_pagination"] = map[string]any{"next_cursor": page.NextCursor}
			}
			return result, nil
		},
	}); err != nil {
		return err
	}
	if err := register(GenericAction{
		Path:                 "journal/read",
		Description:          "Read one canonical journal entry by its JRN key.",
		InputSchema:          journalReadSchema(),
		ExecutionInputSchema: journalReadSchema(),
		OutputSchema:         journalReadOutputSchema(),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.JournalReadInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			entry, err := s.Service.JournalRead(ctx, in)
			if err != nil {
				return nil, err
			}
			return journalEntryPublicProjection(entry), nil
		},
	}); err != nil {
		return err
	}
	return nil
}

func journalEntryPublicProjection(entry model.JournalEntry) map[string]any {
	var data any
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		data = map[string]any{}
	}
	return map[string]any{
		"key":        entry.ID,
		"stream":     string(entry.Stream),
		"data":       data,
		"actor":      entry.Actor,
		"role":       entry.Role,
		"session":    entry.SessionID,
		"sequence":   entry.Sequence,
		"created_at": entry.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z"),
	}
}

func journalStreamPropertySchema() map[string]any {
	streams := model.JournalStreams()
	enum := make([]any, 0, len(streams))
	for _, stream := range streams {
		enum = append(enum, string(stream))
	}
	return map[string]any{
		"type":        "string",
		"description": "Server-owned closed journal stream identifier.",
		"enum":        enum,
		"minLength":   1,
		"maxLength":   model.MaxJournalStreamBytes,
	}
}

func journalContractSchema() map[string]any {
	return obj(map[string]any{"stream": journalStreamPropertySchema()}, "stream")
}

func journalAddSchema() map[string]any {
	return obj(map[string]any{
		"stream": journalStreamPropertySchema(),
		"data":   map[string]any{"type": "object", "description": "Closed stream data contract; see journal/contract."},
	}, "stream", "data")
}

func journalListSchema() map[string]any {
	stream := journalStreamPropertySchema()
	return obj(map[string]any{
		"stream": stream,
		"limit":  integer("Maximum journal entries", 1, 256),
		"cursor": str("Opaque keyset continuation cursor."),
	})
}

func journalReadSchema() map[string]any {
	return obj(map[string]any{
		"key": map[string]any{"type": "string", "pattern": model.JournalIDPattern, "minLength": 8, "maxLength": 23, "description": "Canonical JRN journal key."},
	}, "key")
}

func journalContractOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"stream":           outputString(),
		"purpose":          outputString(),
		"data_schema":      map[string]any{"type": "object"},
		"guide":            outputString(),
		"writer_authority": outputArray(outputString()),
		"limits": map[string]any{
			"type":       "object",
			"properties": map[string]any{"max_data_bytes": outputInteger(), "max_string_bytes": outputInteger(), "max_array_items": outputInteger(), "max_array_item_bytes": outputInteger()},
		},
	}, "stream", "purpose", "data_schema", "guide", "writer_authority", "limits")
}

func journalEntryOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":        outputString(),
			"stream":     outputString(),
			"data":       map[string]any{"type": "object"},
			"actor":      outputString(),
			"role":       outputString(),
			"session":    outputString(),
			"sequence":   outputInteger(),
			"created_at": outputString(),
		},
		"required": []string{"key", "stream", "data", "actor", "role", "session", "sequence", "created_at"},
	}
}

func journalAddOutputSchema() map[string]any {
	return closedOutput(map[string]any{"key": outputString()}, "key")
}

func journalListOutputSchema() map[string]any {
	properties := map[string]any{"items": outputArray(journalEntryOutputSchema())}
	properties["_pagination"] = map[string]any{"type": "object", "properties": map[string]any{"next_cursor": outputString()}}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"items"}}
}

func journalReadOutputSchema() map[string]any {
	return journalEntryOutputSchema()
}
