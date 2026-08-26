package service

import "fmt"

func validateTaskFinalizeSemantics(in TaskFinalizeInput) error {
	if len([]byte(in.Summary)) > 4096 || len(in.AcceptanceCoverage) > 128 || len(in.Deviations) > 128 || len(in.RemainingRisks) > 128 {
		return fmt.Errorf("Task finalize semantic data exceeds bounds")
	}
	for _, values := range [][]string{in.AcceptanceCoverage, in.Deviations, in.RemainingRisks} {
		for _, value := range values {
			if len([]byte(value)) > 1024 {
				return fmt.Errorf("Task finalize semantic value exceeds bounds")
			}
		}
	}
	return nil
}
