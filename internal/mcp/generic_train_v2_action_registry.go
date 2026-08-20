package mcp

import trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"

func (s *Server) ensureTrainV2Actions() {
	s.trainV2Actions.Do(func() {
		s.trainV2ActionErr = s.registerTrainV2Actions()
	})
	if s.trainV2ActionErr != nil {
		panic(s.trainV2ActionErr)
	}
}
func (s *Server) validateTrainV2ActionRegistry() error {
	registered := make([]string, 0, len(s.genericActions))
	for path := range s.genericActions {
		registered = append(registered, path)
	}
	return trainv2.ValidateActionRegistry(trainv2.RequiredCutoverActions, registered)
}
func (s *Server) registerTrainV2Actions() error {
	if err := s.registerTrainV2ActionSet1(); err != nil {
		return err
	}
	if err := s.registerTrainV2ActionSet2(); err != nil {
		return err
	}
	if err := s.registerTrainV2ActionSet3(); err != nil {
		return err
	}
	if err := s.registerTrainV2ActionSet4(); err != nil {
		return err
	}
	return nil
}
