package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) addHandoffTools(add toolAdder) {
	listLimit := integer("Maximum collection items", 1, service.MaxPublicCollectionLimit)
	listLimit["default"] = service.DefaultPublicCollectionLimit
	add("delivery_handoff_publish", "Planner-authorized publication of one durable Delivery handoff; replacements require delivery_handoff_supersede.", deliveryHandoffCreateSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequirePlanner(ctx); err != nil {
			return nil, err
		}
		var in service.DeliveryHandoffCreateInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoff, operation, err := s.Service.DeliveryHandoffCreate(ctx, in)
		return map[string]any{"handoff": handoff, "operation": operation}, err
	})
	add("delivery_handoff_read", "Read one complete durable Delivery handoff.", obj(map[string]any{"handoff_id": str("Handoff identifier")}, "handoff_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "handoff_id")
		if err != nil {
			return nil, err
		}
		return s.Service.DeliveryHandoffRead(ctx, id)
	})
	add("delivery_handoff_status", "Read one bounded durable Delivery handoff status projection.", obj(map[string]any{"handoff_id": str("Handoff identifier")}, "handoff_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "handoff_id")
		if err != nil {
			return nil, err
		}
		return s.Service.DeliveryHandoffStatus(ctx, id)
	})
	add("delivery_handoff_list", "List bounded durable Delivery handoff status projections with deterministic continuation.", obj(map[string]any{"project_id": str("Project identifier"), "limit": listLimit, "cursor": str("Opaque continuation cursor")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.DeliveryHandoffListInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoffs, err := s.Service.DeliveryHandoffListPage(ctx, in)
		return handoffs, err
	})
	add("delivery_handoff_acknowledge", "Delivery-authorized acknowledgement of a pending durable handoff.", obj(map[string]any{"handoff_id": str("Handoff identifier"), "acknowledged_by": str("Delivery identity"), "expected_hub_revision": str("Optimistic Hub revision")}, "handoff_id", "acknowledged_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequireDelivery(ctx); err != nil {
			return nil, err
		}
		var in service.DeliveryHandoffAcknowledgeInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoff, operation, err := s.Service.DeliveryHandoffAcknowledge(ctx, in)
		return map[string]any{"handoff": handoff, "operation": operation}, err
	})
	add("delivery_handoff_next", "Delivery-authorized transition of an acknowledged handoff into execution.", obj(map[string]any{"handoff_id": str("Handoff identifier"), "next_by": str("Delivery identity"), "expected_hub_revision": str("Optimistic Hub revision")}, "handoff_id", "next_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequireDelivery(ctx); err != nil {
			return nil, err
		}
		var in service.DeliveryHandoffNextInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoff, operation, err := s.Service.DeliveryHandoffNext(ctx, in)
		return map[string]any{"handoff": handoff, "operation": operation}, err
	})
	add("delivery_handoff_cancel", "Planner-authorized cancellation of a durable handoff.", obj(map[string]any{"handoff_id": str("Handoff identifier"), "cancelled_by": str("Planner identity"), "reason": str("Cancellation reason"), "expected_hub_revision": str("Optimistic Hub revision")}, "handoff_id", "cancelled_by", "reason"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequirePlanner(ctx); err != nil {
			return nil, err
		}
		var in service.DeliveryHandoffCancelInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoff, operation, err := s.Service.DeliveryHandoffCancel(ctx, in)
		return map[string]any{"handoff": handoff, "operation": operation}, err
	})
	add("delivery_handoff_supersede", "Planner-authorized atomic replacement of one durable handoff.", deliveryHandoffSupersedeSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequirePlanner(ctx); err != nil {
			return nil, err
		}
		var in service.DeliveryHandoffSupersedeInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		handoff, operation, err := s.Service.DeliveryHandoffSupersede(ctx, in)
		return map[string]any{"handoff": handoff, "operation": operation}, err
	})
	add("planner_report_publish", "Delivery-authorized publication of one immutable Planner report.", obj(map[string]any{"handoff_id": str("Handoff identifier"), "report": plannerReportInputSchema(), "expected_hub_revision": str("Optimistic Hub revision")}, "handoff_id", "report"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequireDelivery(ctx); err != nil {
			return nil, err
		}
		var in service.PlannerReportPublishInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		report, operation, err := s.Service.PlannerReportPublish(ctx, in)
		return map[string]any{"report": report, "operation": operation}, err
	})
	add("planner_report_read", "Read one complete immutable Planner report.", obj(map[string]any{"report_id": str("Report identifier")}, "report_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "report_id")
		if err != nil {
			return nil, err
		}
		return s.Service.PlannerReportRead(ctx, id)
	})
	add("planner_report_status", "Read one bounded Planner report status projection bound to its immutable report and state digest.", obj(map[string]any{"report_id": str("Report identifier")}, "report_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, err := getString(raw, "report_id")
		if err != nil {
			return nil, err
		}
		return s.Service.PlannerReportStatus(ctx, id)
	})
	add("planner_report_list", "List bounded Planner report status projections with deterministic continuation.", obj(map[string]any{"project_id": str("Project identifier"), "limit": listLimit, "cursor": str("Opaque continuation cursor")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlannerReportListInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		reports, err := s.Service.PlannerReportListPage(ctx, in)
		return reports, err
	})
	add("planner_report_acknowledge", "Planner-authorized acknowledgement of a published Planner report.", obj(map[string]any{"report_id": str("Report identifier"), "acknowledged_by": str("Planner identity"), "expected_hub_revision": str("Optimistic Hub revision")}, "report_id", "acknowledged_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequirePlanner(ctx); err != nil {
			return nil, err
		}
		var in service.PlannerReportAcknowledgeInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		state, operation, err := s.Service.PlannerReportAcknowledge(ctx, in)
		return map[string]any{"state": state, "operation": operation}, err
	})
	add("planner_report_next", "Planner-authorized resolution of an acknowledged Planner report.", obj(map[string]any{"report_id": str("Report identifier"), "resolved_by": str("Planner identity"), "expected_hub_revision": str("Optimistic Hub revision")}, "report_id", "resolved_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := authority.RequirePlanner(ctx); err != nil {
			return nil, err
		}
		var in service.PlannerReportNextInput
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		state, operation, err := s.Service.PlannerReportNext(ctx, in)
		return map[string]any{"state": state, "operation": operation}, err
	})
}
