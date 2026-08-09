package translate

import (
	"sort"
)

func (s *Translator) finalize() Result {
	s.translation.GraphID = s.graphID
	for _, part := range s.query.Parts {
		if part.mutations != nil && (part.mutations.Creations.Len() > 0 || part.mutations.EdgeCreations.Len() > 0 || part.mutations.Updates.Len() > 0 || part.mutations.Deletions.Len() > 0) {
			s.translation.HasMutations = true
		}
	}
	for kindID := range s.requiredEdgeKindIDs {
		s.translation.RequiredEdgeKindIDs = append(s.translation.RequiredEdgeKindIDs, kindID)
	}
	sort.Slice(s.translation.RequiredEdgeKindIDs, func(i, j int) bool {
		return s.translation.RequiredEdgeKindIDs[i] < s.translation.RequiredEdgeKindIDs[j]
	})
	return s.translation
}
