package mcp

// Legacy underscore task lifecycle tools are intentionally not registered.
// Canonical task/* actions are owned by registerTaskAuthoringActions.
func (s *Server) addTaskTools(_ toolAdder) {}
