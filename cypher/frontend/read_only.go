package frontend

import (
	"errors"

	"github.com/specterops/dawgs/cypher/models/cypher"
)

var ErrMutationNotAllowed = errors.New("Cypher mutation is not allowed in this read-only operation")

// RequireReadOnly rejects every frontend update-clause shape before kind
// mapping, SQL translation, planning, or execution. Callers that execute a
// translated representation must still check the translator's independent
// HasMutations result immediately before execution.
func RequireReadOnly(query *cypher.RegularQuery) error {
	if query == nil || query.SingleQuery == nil {
		return nil
	}
	if query.SingleQuery.SinglePartQuery != nil && len(query.SingleQuery.SinglePartQuery.UpdatingClauses) != 0 {
		return ErrMutationNotAllowed
	}
	if multi := query.SingleQuery.MultiPartQuery; multi != nil {
		for _, part := range multi.Parts {
			if part != nil && len(part.UpdatingClauses) != 0 {
				return ErrMutationNotAllowed
			}
		}
		if multi.SinglePartQuery != nil && len(multi.SinglePartQuery.UpdatingClauses) != 0 {
			return ErrMutationNotAllowed
		}
	}
	return nil
}
