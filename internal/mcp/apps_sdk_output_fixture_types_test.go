package mcp

import (
	"time"
)

type canonicalOutputFixture struct {
	now                                                                                                            time.Time
	project, plan, section, render, adr, policy, task, state                                                       any
	run, transaction, operation, local, worktree, report, packet, publicRun, publicPacket                          any
	sessionID                                                                                                      string
	operatorEvent, operatorHistory, ref, commit, compare                                                           any
	clean                                                                                                          bool
	reviewState, reviewReport, reviewDraft, reviewValidation, snapshot                                             any
	handoffSummary, handoff, handoffStatus, reportEvidence, plannerReport, plannerReportStatus, plannerReportState any
	revisionSample, revisionStatusSample                                                                           any
}

func newCanonicalOutputFixture() canonicalOutputFixture {
	f := canonicalOutputFixture{now: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)}
	f.populateCanonicalProjectPlan()
	f.populateCanonicalExecution()
	f.populateCanonicalReview()
	f.populateCanonicalHandoff()
	return f
}
