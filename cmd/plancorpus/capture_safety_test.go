package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanCaptureRejectsMutationBeforeBackendAccess(t *testing.T) {
	for _, driver := range []string{"pg", "neo4j"} {
		t.Run(driver, func(t *testing.T) {
			record := PlanRecord{}
			capture := backendCapture{}
			if driver == "pg" {
				capture.capturePostgres(context.Background(), `create (n:Thing) return n`, nil, &record)
			} else {
				capture.captureNeo4j(`create (n:Thing) return n`, nil, &record)
			}
			require.Contains(t, record.Error, "mutation is not allowed")
		})
	}
}
